// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/juju/juju/apiserver/facade (interfaces: RaftContext,Authorizer)

// Package raftlease is a generated GoMock package.
package raftlease

import (
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	permission "github.com/juju/juju/core/permission"
	names "github.com/juju/names/v4"
)

// MockRaftContext is a mock of RaftContext interface.
type MockRaftContext struct {
	ctrl     *gomock.Controller
	recorder *MockRaftContextMockRecorder
}

// MockRaftContextMockRecorder is the mock recorder for MockRaftContext.
type MockRaftContextMockRecorder struct {
	mock *MockRaftContext
}

// NewMockRaftContext creates a new mock instance.
func NewMockRaftContext(ctrl *gomock.Controller) *MockRaftContext {
	mock := &MockRaftContext{ctrl: ctrl}
	mock.recorder = &MockRaftContextMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockRaftContext) EXPECT() *MockRaftContextMockRecorder {
	return m.recorder
}

// ApplyLease mocks base method.
func (m *MockRaftContext) ApplyLease(arg0 []byte) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ApplyLease", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// ApplyLease indicates an expected call of ApplyLease.
func (mr *MockRaftContextMockRecorder) ApplyLease(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ApplyLease", reflect.TypeOf((*MockRaftContext)(nil).ApplyLease), arg0)
}

// MockAuthorizer is a mock of Authorizer interface.
type MockAuthorizer struct {
	ctrl     *gomock.Controller
	recorder *MockAuthorizerMockRecorder
}

// MockAuthorizerMockRecorder is the mock recorder for MockAuthorizer.
type MockAuthorizerMockRecorder struct {
	mock *MockAuthorizer
}

// NewMockAuthorizer creates a new mock instance.
func NewMockAuthorizer(ctrl *gomock.Controller) *MockAuthorizer {
	mock := &MockAuthorizer{ctrl: ctrl}
	mock.recorder = &MockAuthorizerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockAuthorizer) EXPECT() *MockAuthorizerMockRecorder {
	return m.recorder
}

// AuthApplicationAgent mocks base method.
func (m *MockAuthorizer) AuthApplicationAgent() bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AuthApplicationAgent")
	ret0, _ := ret[0].(bool)
	return ret0
}

// AuthApplicationAgent indicates an expected call of AuthApplicationAgent.
func (mr *MockAuthorizerMockRecorder) AuthApplicationAgent() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AuthApplicationAgent", reflect.TypeOf((*MockAuthorizer)(nil).AuthApplicationAgent))
}

// AuthClient mocks base method.
func (m *MockAuthorizer) AuthClient() bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AuthClient")
	ret0, _ := ret[0].(bool)
	return ret0
}

// AuthClient indicates an expected call of AuthClient.
func (mr *MockAuthorizerMockRecorder) AuthClient() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AuthClient", reflect.TypeOf((*MockAuthorizer)(nil).AuthClient))
}

// AuthController mocks base method.
func (m *MockAuthorizer) AuthController() bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AuthController")
	ret0, _ := ret[0].(bool)
	return ret0
}

// AuthController indicates an expected call of AuthController.
func (mr *MockAuthorizerMockRecorder) AuthController() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AuthController", reflect.TypeOf((*MockAuthorizer)(nil).AuthController))
}

// AuthMachineAgent mocks base method.
func (m *MockAuthorizer) AuthMachineAgent() bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AuthMachineAgent")
	ret0, _ := ret[0].(bool)
	return ret0
}

// AuthMachineAgent indicates an expected call of AuthMachineAgent.
func (mr *MockAuthorizerMockRecorder) AuthMachineAgent() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AuthMachineAgent", reflect.TypeOf((*MockAuthorizer)(nil).AuthMachineAgent))
}

// AuthModelAgent mocks base method.
func (m *MockAuthorizer) AuthModelAgent() bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AuthModelAgent")
	ret0, _ := ret[0].(bool)
	return ret0
}

// AuthModelAgent indicates an expected call of AuthModelAgent.
func (mr *MockAuthorizerMockRecorder) AuthModelAgent() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AuthModelAgent", reflect.TypeOf((*MockAuthorizer)(nil).AuthModelAgent))
}

// AuthOwner mocks base method.
func (m *MockAuthorizer) AuthOwner(arg0 names.Tag) bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AuthOwner", arg0)
	ret0, _ := ret[0].(bool)
	return ret0
}

// AuthOwner indicates an expected call of AuthOwner.
func (mr *MockAuthorizerMockRecorder) AuthOwner(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AuthOwner", reflect.TypeOf((*MockAuthorizer)(nil).AuthOwner), arg0)
}

// AuthUnitAgent mocks base method.
func (m *MockAuthorizer) AuthUnitAgent() bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AuthUnitAgent")
	ret0, _ := ret[0].(bool)
	return ret0
}

// AuthUnitAgent indicates an expected call of AuthUnitAgent.
func (mr *MockAuthorizerMockRecorder) AuthUnitAgent() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AuthUnitAgent", reflect.TypeOf((*MockAuthorizer)(nil).AuthUnitAgent))
}

// ConnectedModel mocks base method.
func (m *MockAuthorizer) ConnectedModel() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ConnectedModel")
	ret0, _ := ret[0].(string)
	return ret0
}

// ConnectedModel indicates an expected call of ConnectedModel.
func (mr *MockAuthorizerMockRecorder) ConnectedModel() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ConnectedModel", reflect.TypeOf((*MockAuthorizer)(nil).ConnectedModel))
}

// GetAuthTag mocks base method.
func (m *MockAuthorizer) GetAuthTag() names.Tag {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetAuthTag")
	ret0, _ := ret[0].(names.Tag)
	return ret0
}

// GetAuthTag indicates an expected call of GetAuthTag.
func (mr *MockAuthorizerMockRecorder) GetAuthTag() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetAuthTag", reflect.TypeOf((*MockAuthorizer)(nil).GetAuthTag))
}

// HasPermission mocks base method.
func (m *MockAuthorizer) HasPermission(arg0 permission.Access, arg1 names.Tag) (bool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "HasPermission", arg0, arg1)
	ret0, _ := ret[0].(bool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// HasPermission indicates an expected call of HasPermission.
func (mr *MockAuthorizerMockRecorder) HasPermission(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "HasPermission", reflect.TypeOf((*MockAuthorizer)(nil).HasPermission), arg0, arg1)
}

// UserHasPermission mocks base method.
func (m *MockAuthorizer) UserHasPermission(arg0 names.UserTag, arg1 permission.Access, arg2 names.Tag) (bool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UserHasPermission", arg0, arg1, arg2)
	ret0, _ := ret[0].(bool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// UserHasPermission indicates an expected call of UserHasPermission.
func (mr *MockAuthorizerMockRecorder) UserHasPermission(arg0, arg1, arg2 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UserHasPermission", reflect.TypeOf((*MockAuthorizer)(nil).UserHasPermission), arg0, arg1, arg2)
}
