// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/scionproto/scion/go/lib/sock/reliable (interfaces: DispatcherService)

// Package mock_reliable is a generated GoMock package.
package mock_reliable

import (
	gomock "github.com/golang/mock/gomock"
	addr "github.com/scionproto/scion/go/lib/addr"
	net "net"
	reflect "reflect"
	time "time"
)

// MockDispatcherService is a mock of DispatcherService interface
type MockDispatcherService struct {
	ctrl     *gomock.Controller
	recorder *MockDispatcherServiceMockRecorder
}

// MockDispatcherServiceMockRecorder is the mock recorder for MockDispatcherService
type MockDispatcherServiceMockRecorder struct {
	mock *MockDispatcherService
}

// NewMockDispatcherService creates a new mock instance
func NewMockDispatcherService(ctrl *gomock.Controller) *MockDispatcherService {
	mock := &MockDispatcherService{ctrl: ctrl}
	mock.recorder = &MockDispatcherServiceMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockDispatcherService) EXPECT() *MockDispatcherServiceMockRecorder {
	return m.recorder
}

// Register mocks base method
func (m *MockDispatcherService) Register(arg0 addr.IA, arg1 *addr.AppAddr, arg2 *net.UDPAddr, arg3 addr.HostSVC) (net.PacketConn, uint16, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Register", arg0, arg1, arg2, arg3)
	ret0, _ := ret[0].(net.PacketConn)
	ret1, _ := ret[1].(uint16)
	ret2, _ := ret[2].(error)
	return ret0, ret1, ret2
}

// Register indicates an expected call of Register
func (mr *MockDispatcherServiceMockRecorder) Register(arg0, arg1, arg2, arg3 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Register", reflect.TypeOf((*MockDispatcherService)(nil).Register), arg0, arg1, arg2, arg3)
}

// RegisterTimeout mocks base method
func (m *MockDispatcherService) RegisterTimeout(arg0 addr.IA, arg1 *addr.AppAddr, arg2 *net.UDPAddr, arg3 addr.HostSVC, arg4 time.Duration) (net.PacketConn, uint16, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "RegisterTimeout", arg0, arg1, arg2, arg3, arg4)
	ret0, _ := ret[0].(net.PacketConn)
	ret1, _ := ret[1].(uint16)
	ret2, _ := ret[2].(error)
	return ret0, ret1, ret2
}

// RegisterTimeout indicates an expected call of RegisterTimeout
func (mr *MockDispatcherServiceMockRecorder) RegisterTimeout(arg0, arg1, arg2, arg3, arg4 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "RegisterTimeout", reflect.TypeOf((*MockDispatcherService)(nil).RegisterTimeout), arg0, arg1, arg2, arg3, arg4)
}
