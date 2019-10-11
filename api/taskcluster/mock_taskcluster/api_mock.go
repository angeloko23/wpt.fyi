// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/web-platform-tests/wpt.fyi/api/taskcluster (interfaces: API)

// Package mock_taskcluster is a generated GoMock package.
package mock_taskcluster

import (
	gomock "github.com/golang/mock/gomock"
	github "github.com/google/go-github/v28/github"
	reflect "reflect"
)

// MockAPI is a mock of API interface
type MockAPI struct {
	ctrl     *gomock.Controller
	recorder *MockAPIMockRecorder
}

// MockAPIMockRecorder is the mock recorder for MockAPI
type MockAPIMockRecorder struct {
	mock *MockAPI
}

// NewMockAPI creates a new mock instance
func NewMockAPI(ctrl *gomock.Controller) *MockAPI {
	mock := &MockAPI{ctrl: ctrl}
	mock.recorder = &MockAPIMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockAPI) EXPECT() *MockAPIMockRecorder {
	return m.recorder
}

// HandleCheckSuiteEvent mocks base method
func (m *MockAPI) HandleCheckSuiteEvent(arg0 *github.CheckSuiteEvent) (bool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "HandleCheckSuiteEvent", arg0)
	ret0, _ := ret[0].(bool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// HandleCheckSuiteEvent indicates an expected call of HandleCheckSuiteEvent
func (mr *MockAPIMockRecorder) HandleCheckSuiteEvent(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "HandleCheckSuiteEvent", reflect.TypeOf((*MockAPI)(nil).HandleCheckSuiteEvent), arg0)
}
