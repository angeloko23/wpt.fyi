// Copyright 2018 The WPT Dashboard Project. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"
	"sync"

	mapset "github.com/deckarep/golang-set"
	"github.com/web-platform-tests/results-analysis/metrics"
	"github.com/web-platform-tests/wpt.fyi/shared"

	log "github.com/sirupsen/logrus"
)

var (
	errNilRun             = errors.New("Test run is nil")
	errNoQuery            = errors.New("No query provided")
	errNoRuns             = errors.New("No runs")
	errRunExists          = errors.New("Run already exists in index")
	errRunLoading         = errors.New("Run currently being loaded into index")
	errSomeShardsRequired = errors.New("Index must have at least one shard")
	errZeroRun            = errors.New("Cannot ingest run with ID of 0")
)

// Index is an index of test run results that can ingest and evict runs.
// FUTURE: Index will also be able to service queries.
type Index interface {
	// IngestRun loads the test run results associated with the input test run
	// into the index.
	IngestRun(shared.TestRun) error
	// EvictAnyRun reduces memory pressure by evicting the cache's choice of run
	// from memory.
	EvictAnyRun() error
}

// ProxyIndex is a proxy implementation of the Index interface. This type is
// generally used in type embeddings that wish to override the behaviour of some
// (but not all) methods, deferring to the delegate for all other behaviours.
type ProxyIndex struct {
	delegate Index
}

// IngestRun loads the given run's results in to the index by deferring ot the
// proxy's delegate.
func (i *ProxyIndex) IngestRun(r shared.TestRun) error {
	return i.delegate.IngestRun(r)
}

// EvictAnyRun deletes one run's results from the index by deferring to the
// proxy's delegate.
func (i *ProxyIndex) EvictAnyRun() error {
	return i.delegate.EvictAnyRun()
}

// NewProxyIndex instantiates a new proxy index bound to the given delegate.
func NewProxyIndex(idx Index) ProxyIndex {
	return ProxyIndex{idx}
}

// ReportLoader handles loading a WPT test results report based on metadata in
// a shared.TestRun.
type ReportLoader interface {
	Load(shared.TestRun) (*metrics.TestResultsReport, error)
}

// shardedWPTIndex is an Index that manages test and result data across mutually
// exclusive shards.
type shardedWPTIndex struct {
	runs     map[RunID]shared.TestRun
	inFlight mapset.Set
	loader   ReportLoader
	shards   []*wptIndex
	m        *sync.RWMutex
}

// wptIndex is an index of tests and results. Multicore machines should use
// shardedWPTIndex, which embed a slice of wptIndex containing mutually
// exclusive subsets of test and result data.
type wptIndex struct {
	tests   Tests
	results Results
}

// testData is a wrapper for a single unit of test+result data from a test run.
type testData struct {
	testName
	ResultID
}

type httpReportLoader struct{}

func (i *shardedWPTIndex) IngestRun(r shared.TestRun) error {
	// Error cases: ID cannot be 0, run cannot be loaded or loading-in-progress.
	if r.ID == 0 {
		return errZeroRun
	}
	if err := i.syncMarkInProgress(r); err != nil {
		return err
	}
	defer i.syncClearInProgress(r)

	// Delegate loader to construct complete run report.
	report, err := i.loader.Load(r)
	if err != nil {
		return err
	}

	// Results of different tests will be stored in different shards, based on the
	// top-level test (i.e., not subtests) integral ID of each test in the report.
	//
	// Create RunResults for each shard's partition of this run's results.
	numShards := len(i.shards)
	numShardsU64 := uint64(numShards)
	shardData := make([]map[TestID]testData, numShards)
	for j := 0; j < numShards; j++ {
		shardData[j] = make(map[TestID]testData)
	}

	for _, res := range report.Results {
		// Add top-level test (i.e., not subtest) result to appropriate shard.
		t, err := computeID(res.Test, nil)
		if err != nil {
			return err
		}

		shardIdx := int(t.testID % numShardsU64)
		dataForShard := shardData[shardIdx]
		re := ResultID(shared.TestStatusValueFromString(res.Status))
		dataForShard[t] = testData{
			testName: testName{
				name:    res.Test,
				subName: nil,
			},
			ResultID: re,
		}

		// Dedup subtests, warning when subtest names are duplicated.
		subs := make(map[string]metrics.SubTest)
		for _, sub := range res.Subtests {
			if _, ok := subs[sub.Name]; ok {
				log.Warningf("Duplicate subtests with the same name: %s %s", res.Test, sub.Name)
				continue
			}
			subs[sub.Name] = sub
		}

		// Add each subtests' result to the appropriate shard (same shard as
		// top-level test).
		for _, sub := range subs {
			t, err := computeID(res.Test, &sub.Name)
			if err != nil {
				return err
			}

			re := ResultID(shared.TestStatusValueFromString(sub.Status))
			dataForShard[t] = testData{
				testName: testName{
					name:    res.Test,
					subName: &sub.Name,
				},
				ResultID: re,
			}
		}
	}

	i.syncStoreRun(r, shardData)

	return nil
}

func (i *shardedWPTIndex) EvictAnyRun() error {
	return i.syncEvictRun()
}

func (l httpReportLoader) Load(run shared.TestRun) (*metrics.TestResultsReport, error) {
	// Attempt to fetch-and-unmarshal run from run.RawResultsURL.
	resp, err := http.Get(run.RawResultsURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(`Non-OK HTTP status code of %d from "%s" for run ID=%d`, resp.StatusCode, run.RawResultsURL, run.ID)
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var report metrics.TestResultsReport
	err = json.Unmarshal(data, &report)
	if err != nil {
		return nil, err
	}
	if len(report.Results) == 0 {
		return nil, fmt.Errorf("Empty report from run ID=%d (%s)", run.ID, run.RawResultsURL)
	}
	return &report, nil
}

// NewShardedWPTIndex creates a new empty Index for WPT test run results.
func NewShardedWPTIndex(loader ReportLoader, numShards int) (Index, error) {
	if numShards <= 0 {
		return nil, errSomeShardsRequired
	}

	shards := make([]*wptIndex, 0, numShards)
	for i := 0; i < numShards; i++ {
		tests := NewTests()
		shards = append(shards, newWPTIndex(tests))
	}

	return &shardedWPTIndex{
		runs:     make(map[RunID]shared.TestRun),
		inFlight: mapset.NewSet(),
		loader:   loader,
		shards:   shards,
		m:        &sync.RWMutex{},
	}, nil
}

// NewReportLoader constructs a loader that loads result reports over HTTP from
// a shared.TestRun.RawResultsURL.
func NewReportLoader() ReportLoader {
	return httpReportLoader{}
}

func (i *shardedWPTIndex) syncMarkInProgress(run shared.TestRun) error {
	i.m.Lock()
	defer i.m.Unlock()

	id := RunID(run.ID)
	_, loaded := i.runs[id]
	if loaded {
		return errRunExists
	}
	if i.inFlight.Contains(id) {
		return errRunLoading
	}

	i.inFlight.Add(id)

	return nil
}

func (i *shardedWPTIndex) syncClearInProgress(run shared.TestRun) error {
	i.m.Lock()
	defer i.m.Unlock()

	id := RunID(run.ID)
	if !i.inFlight.Contains(id) {
		return errNilRun
	}

	i.inFlight.Remove(id)

	return nil
}

func (i *shardedWPTIndex) syncStoreRun(run shared.TestRun, data []map[TestID]testData) error {
	i.m.Lock()
	defer i.m.Unlock()

	id := RunID(run.ID)
	for j, shardData := range data {
		runResults := NewRunResults()
		for t, data := range shardData {
			i.shards[j].tests.Add(t, data.testName.name, data.testName.subName)
			runResults.Add(data.ResultID, t)
		}
		err := i.shards[j].results.Add(id, runResults)
		if err != nil {
			return err
		}
	}
	i.runs[id] = run

	return nil
}

func (i *shardedWPTIndex) syncEvictRun() error {
	i.m.Lock()
	defer i.m.Unlock()

	if len(i.runs) == 0 {
		return errNoRuns
	}

	// Accumulate runs into sortable collection.
	runs := make(shared.TestRuns, 0, len(i.runs))
	for _, run := range i.runs {
		runs = append(runs, run)
	}

	// Sort and mark oldest run for eviction.
	sort.Sort(runs)
	id := RunID(runs[0].ID)

	// Delete data from shards, and from runs collection.
	for _, shard := range i.shards {
		err := shard.results.Delete(id)
		if err != nil {
			return err
		}
	}
	delete(i.runs, id)

	return nil
}

func (i *shardedWPTIndex) syncExtractRuns(ids []RunID) ([]index, error) {
	i.m.RLock()
	defer i.m.RUnlock()

	idxs := make([]index, len(i.shards))
	for j, shard := range i.shards {
		tests := shard.tests
		runResults := make(map[RunID]RunResults)
		for _, id := range ids {
			rrs := shard.results.ForRun(id)
			if rrs == nil {
				return nil, fmt.Errorf("Run is unknown to shard: RunID=%v", id)
			}
			runResults[id] = shard.results.ForRun(id)
		}
		idxs[j] = index{
			tests:      tests,
			runResults: runResults,
		}
	}

	return idxs, nil
}

func newWPTIndex(tests Tests) *wptIndex {
	return &wptIndex{
		tests:   tests,
		results: NewResults(),
	}
}