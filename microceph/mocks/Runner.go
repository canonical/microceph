// Code generated by mockery v2.30.10. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"
)

// Runner is an autogenerated mock type for the Runner type
type Runner struct {
	mock.Mock
}

// RunCommand provides a mock function with given fields: name, arg
func (_m *Runner) RunCommand(name string, arg ...string) (string, error) {
	_va := make([]interface{}, len(arg))
	for _i := range arg {
		_va[_i] = arg[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, name)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func(string, ...string) (string, error)); ok {
		return rf(name, arg...)
	}
	if rf, ok := ret.Get(0).(func(string, ...string) string); ok {
		r0 = rf(name, arg...)
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func(string, ...string) error); ok {
		r1 = rf(name, arg...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// RunCommandContext provides a mock function with given fields: ctx, name, arg
func (_m *Runner) RunCommandContext(ctx context.Context, name string, arg ...string) (string, error) {
	_va := make([]interface{}, len(arg))
	for _i := range arg {
		_va[_i] = arg[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, ctx, name)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, ...string) (string, error)); ok {
		return rf(ctx, name, arg...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, ...string) string); ok {
		r0 = rf(ctx, name, arg...)
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, ...string) error); ok {
		r1 = rf(ctx, name, arg...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// NewRunner creates a new instance of Runner. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewRunner(t interface {
	mock.TestingT
	Cleanup(func())
}) *Runner {
	mock := &Runner{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
