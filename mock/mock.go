// Code generated by MockGen. DO NOT EDIT.
// Source: telegram.go

// Package mock_app is a generated GoMock package.
package mock_app

import (
	AI "Antispam/AI"
	reflect "reflect"
	time "time"

	gomock "github.com/golang/mock/gomock"
)

// MockIRedis is a mock of IRedis interface.
type MockIRedis struct {
	ctrl     *gomock.Controller
	recorder *MockIRedisMockRecorder
}

// MockIRedisMockRecorder is the mock recorder for MockIRedis.
type MockIRedisMockRecorder struct {
	mock *MockIRedis
}

// NewMockIRedis creates a new mock instance.
func NewMockIRedis(ctrl *gomock.Controller) *MockIRedis {
	mock := &MockIRedis{ctrl: ctrl}
	mock.recorder = &MockIRedisMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockIRedis) EXPECT() *MockIRedisMockRecorder {
	return m.recorder
}

// AppendItems mocks base method.
func (m *MockIRedis) AppendItems(key, value string) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "AppendItems", key, value)
}

// AppendItems indicates an expected call of AppendItems.
func (mr *MockIRedisMockRecorder) AppendItems(key, value interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AppendItems", reflect.TypeOf((*MockIRedis)(nil).AppendItems), key, value)
}

// Delete mocks base method.
func (m *MockIRedis) Delete(key string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Delete", key)
	ret0, _ := ret[0].(error)
	return ret0
}

// Delete indicates an expected call of Delete.
func (mr *MockIRedisMockRecorder) Delete(key interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Delete", reflect.TypeOf((*MockIRedis)(nil).Delete), key)
}

// DeleteItems mocks base method.
func (m *MockIRedis) DeleteItems(key, value string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteItems", key, value)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeleteItems indicates an expected call of DeleteItems.
func (mr *MockIRedisMockRecorder) DeleteItems(key, value interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteItems", reflect.TypeOf((*MockIRedis)(nil).DeleteItems), key, value)
}

// Get mocks base method.
func (m *MockIRedis) Get(key string) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Get", key)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Get indicates an expected call of Get.
func (mr *MockIRedisMockRecorder) Get(key interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Get", reflect.TypeOf((*MockIRedis)(nil).Get), key)
}

// Items mocks base method.
func (m *MockIRedis) Items(key string) ([]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Items", key)
	ret0, _ := ret[0].([]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Items indicates an expected call of Items.
func (mr *MockIRedisMockRecorder) Items(key interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Items", reflect.TypeOf((*MockIRedis)(nil).Items), key)
}

// KeyExists mocks base method.
func (m *MockIRedis) KeyExists(key string) bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "KeyExists", key)
	ret0, _ := ret[0].(bool)
	return ret0
}

// KeyExists indicates an expected call of KeyExists.
func (mr *MockIRedisMockRecorder) KeyExists(key interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "KeyExists", reflect.TypeOf((*MockIRedis)(nil).KeyExists), key)
}

// Keys mocks base method.
func (m *MockIRedis) Keys() []string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Keys")
	ret0, _ := ret[0].([]string)
	return ret0
}

// Keys indicates an expected call of Keys.
func (mr *MockIRedisMockRecorder) Keys() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Keys", reflect.TypeOf((*MockIRedis)(nil).Keys))
}

// KeysMask mocks base method.
func (m *MockIRedis) KeysMask(mask string) []string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "KeysMask", mask)
	ret0, _ := ret[0].([]string)
	return ret0
}

// KeysMask indicates an expected call of KeysMask.
func (mr *MockIRedisMockRecorder) KeysMask(mask interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "KeysMask", reflect.TypeOf((*MockIRedis)(nil).KeysMask), mask)
}

// Set mocks base method.
func (m *MockIRedis) Set(key, value string, ttl time.Duration) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Set", key, value, ttl)
	ret0, _ := ret[0].(error)
	return ret0
}

// Set indicates an expected call of Set.
func (mr *MockIRedisMockRecorder) Set(key, value, ttl interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Set", reflect.TypeOf((*MockIRedis)(nil).Set), key, value, ttl)
}

// SetMap mocks base method.
func (m *MockIRedis) SetMap(key string, value map[string]string) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "SetMap", key, value)
}

// SetMap indicates an expected call of SetMap.
func (mr *MockIRedisMockRecorder) SetMap(key, value interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SetMap", reflect.TypeOf((*MockIRedis)(nil).SetMap), key, value)
}

// StringMap mocks base method.
func (m *MockIRedis) StringMap(key string) (map[string]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "StringMap", key)
	ret0, _ := ret[0].(map[string]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// StringMap indicates an expected call of StringMap.
func (mr *MockIRedisMockRecorder) StringMap(key interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "StringMap", reflect.TypeOf((*MockIRedis)(nil).StringMap), key)
}

// MockIMessageAnalysis is a mock of IMessageAnalysis interface.
type MockIMessageAnalysis struct {
	ctrl     *gomock.Controller
	recorder *MockIMessageAnalysisMockRecorder
}

// MockIMessageAnalysisMockRecorder is the mock recorder for MockIMessageAnalysis.
type MockIMessageAnalysisMockRecorder struct {
	mock *MockIMessageAnalysis
}

// NewMockIMessageAnalysis creates a new mock instance.
func NewMockIMessageAnalysis(ctrl *gomock.Controller) *MockIMessageAnalysis {
	mock := &MockIMessageAnalysis{ctrl: ctrl}
	mock.recorder = &MockIMessageAnalysisMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockIMessageAnalysis) EXPECT() *MockIMessageAnalysisMockRecorder {
	return m.recorder
}

// GetMessageCharacteristics mocks base method.
func (m *MockIMessageAnalysis) GetMessageCharacteristics(msgText string) (*AI.MessageAnalysis, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetMessageCharacteristics", msgText)
	ret0, _ := ret[0].(*AI.MessageAnalysis)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetMessageCharacteristics indicates an expected call of GetMessageCharacteristics.
func (mr *MockIMessageAnalysisMockRecorder) GetMessageCharacteristics(msgText interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetMessageCharacteristics", reflect.TypeOf((*MockIMessageAnalysis)(nil).GetMessageCharacteristics), msgText)
}
