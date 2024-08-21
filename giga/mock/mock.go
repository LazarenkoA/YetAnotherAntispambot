// Code generated by MockGen. DO NOT EDIT.
// Source: client.go

// Package mock_giga is a generated GoMock package.
package mock_giga

import (
	context "context"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	gigachat "github.com/paulrzcz/go-gigachat"
)

// MockIGigaClient is a mock of IGigaClient interface.
type MockIGigaClient struct {
	ctrl     *gomock.Controller
	recorder *MockIGigaClientMockRecorder
}

// MockIGigaClientMockRecorder is the mock recorder for MockIGigaClient.
type MockIGigaClientMockRecorder struct {
	mock *MockIGigaClient
}

// NewMockIGigaClient creates a new mock instance.
func NewMockIGigaClient(ctrl *gomock.Controller) *MockIGigaClient {
	mock := &MockIGigaClient{ctrl: ctrl}
	mock.recorder = &MockIGigaClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockIGigaClient) EXPECT() *MockIGigaClientMockRecorder {
	return m.recorder
}

// AuthWithContext mocks base method.
func (m *MockIGigaClient) AuthWithContext(ctx context.Context) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AuthWithContext", ctx)
	ret0, _ := ret[0].(error)
	return ret0
}

// AuthWithContext indicates an expected call of AuthWithContext.
func (mr *MockIGigaClientMockRecorder) AuthWithContext(ctx interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AuthWithContext", reflect.TypeOf((*MockIGigaClient)(nil).AuthWithContext), ctx)
}

// ChatWithContext mocks base method.
func (m *MockIGigaClient) ChatWithContext(ctx context.Context, in *gigachat.ChatRequest) (*gigachat.ChatResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ChatWithContext", ctx, in)
	ret0, _ := ret[0].(*gigachat.ChatResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ChatWithContext indicates an expected call of ChatWithContext.
func (mr *MockIGigaClientMockRecorder) ChatWithContext(ctx, in interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ChatWithContext", reflect.TypeOf((*MockIGigaClient)(nil).ChatWithContext), ctx, in)
}
