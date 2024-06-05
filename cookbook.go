package kitchen

import (
	"sync"
	"sync/atomic"
)

//func (e *cookbook[M]) BeforeExec(handler BeforeListenHandler[M]) *cookbook[M] {
//	e.beforeListenHandlers = append(e.beforeListenHandlers, handler)
//	return e
//}

// cookbook is the base struct for dishes, sets and menus
type cookbook[D ICookware, I any, O any] struct {
	//beforeListenHandlers []BeforeListenHandler[M]
	instance                      IInstance
	afterListenHandlers           []AfterListenHandlers[D, I, O]
	afterListenHandlersExtra      [][]any
	asyncAfterListenHandlers      []AfterListenHandlers[D, I, O]
	asyncAfterListenHandlersExtra [][]any
	inherited                     []iCookbook[D]
	concurrentLimit               *int32
	running                       *int32
	spinLocker                    *sync.Mutex
	runningLock                   *sync.Mutex
	nodes                         []iCookbook[D]
	checkIfLock                   func() func()
	checkIfLockThis               func() func()
	fullName                      string
	isTraceable                   bool
	isInheritableCookware         bool
}

var (
	nilIfLock = func() func() {
		return nil
	}
)

func (r *cookbook[D, I, O]) init() {
	r.checkIfLock = nilIfLock
	r.checkIfLockThis = nilIfLock
}

func (r cookbook[D, I, O]) Menu() IMenu {
	return r.instance.Menu()
}

func (b cookbook[D, I, O]) isTraceableDep() bool {
	return b.isTraceable
}

func (b cookbook[D, I, O]) isInheritableDep() bool {
	return b.isInheritableCookware
}

// AfterExec aliases of AfterCook
func (r *cookbook[D, I, O]) AfterExec(handler AfterListenHandlers[D, I, O], toLog ...any) *cookbook[D, I, O] {
	return r.AfterCook(handler, toLog...)
}

// AfterExec registers a handler to be called after the dish has been executed.
func (r *cookbook[D, I, O]) AfterCook(handler AfterListenHandlers[D, I, O], toLog ...any) *cookbook[D, I, O] {
	r.afterListenHandlers = append(r.afterListenHandlers, handler)
	r.afterListenHandlersExtra = append(r.afterListenHandlersExtra, toLog)
	return r
}

// AfterExecAsync aliases of AfterCookAsync
func (r *cookbook[D, I, O]) AfterExecAsync(handler AfterListenHandlers[D, I, O], toLog ...any) *cookbook[D, I, O] {
	return r.AfterCookAsync(handler, toLog...)
}

// AfterExecAsync registers a handler to be called after the dish has been executed asynchronously.
func (r *cookbook[D, I, O]) AfterCookAsync(handler AfterListenHandlers[D, I, O], toLog ...any) *cookbook[D, I, O] {
	r.asyncAfterListenHandlers = append(r.asyncAfterListenHandlers, handler)
	r.asyncAfterListenHandlersExtra = append(r.asyncAfterListenHandlersExtra, toLog)
	return r
}

func (r *cookbook[D, I, O]) inherit(ev ...iCookbook[D]) {
	r.inherited = append(r.inherited, ev...)
}

func (r cookbook[D, I, O]) emitAfterCook(ctx IContext[D], input, output any, err error) {
	if l := len(r.asyncAfterListenHandlers); l+len(r.afterListenHandlers) != 0 {
		if l != 0 {
			ctx.servedWeb()
			go func() {
				var (
					cbCtx = ctx
					t     iTraceSpan[D]
				)
				for i, handler := range r.asyncAfterListenHandlers {
					cbCtx, t = cbCtx.logSideEffect(r.instance.Name(), r.asyncAfterListenHandlersExtra[i])
					handler(cbCtx, input.(I), output.(O), err)
					if t != nil {
						t.End(nil, nil)
					}
				}
			}()
		}
		var (
			cbCtx = ctx
			t     iTraceSpan[D]
		)
		for i, handler := range r.afterListenHandlers {
			cbCtx, t = cbCtx.logSideEffect(r.instance.Name(), r.afterListenHandlersExtra[i])
			handler(cbCtx, input.(I), output.(O), err)
			if t != nil {
				t.End(nil, nil)
			}
		}
	}
	for _, ev := range r.inherited {
		ev.emitAfterCook(ctx, input, output, err)
	}
}

// ConcurrentLimit sets the maximum number of concurrent executions of this node and it's children.
func (r *cookbook[D, I, O]) ConcurrentLimit(limit int32) {
	if atomic.LoadInt32(r.concurrentLimit) < limit {
		defer func() {
			if atomic.LoadInt32(r.running) < limit {
				r.spinLocker.TryLock()
				r.spinLocker.Unlock()
			}
		}()
	}
	atomic.StoreInt32(r.concurrentLimit, limit)
	if limit != 0 {
		r.checkIfLock = r._ifLock
		r.checkIfLockThis = r._ifLockThis
	} else {
		r.checkIfLock = nilIfLock
		r.checkIfLockThis = nilIfLock
	}
}

func (r *cookbook[D, I, O]) ifLock() func() {
	return r.checkIfLock()
}

func (r *cookbook[D, I, O]) _ifLock() func() {
	if limit := atomic.LoadInt32(r.concurrentLimit); limit != 0 {
		if atomic.AddInt32(r.running, 1) >= limit {
			r.spinLocker.Lock()
		}
		return r.releaseLimit
	}
	return nil
}

func (r *cookbook[D, I, O]) ifLockThis() func() {
	return r.checkIfLockThis()
}

func (r *cookbook[D, I, O]) _ifLockThis() func() {
	var (
		unlock  func()
		unlocks = make([]func(), 0, len(r.inherited)+1)
	)
	for _, inherited := range r.inherited {
		unlock = inherited.ifLock()
		if unlock != nil {
			unlocks = append(unlocks, unlock)
		}
	}
	if unlock = r.ifLock(); unlock != nil {
		unlocks = append(unlocks, unlock)
	}
	if len(unlocks) == 0 {
		return nil
	} else if len(unlocks) == 1 {
		return unlocks[0]
	} else {
		return func() {
			for _, unlock := range unlocks {
				unlock()
			}
		}
	}
}

// Nodes returns the children of this node.
func (b cookbook[D, I, O]) Nodes() []IInstance {
	res := make([]IInstance, len(b.nodes))
	for i, n := range b.nodes {
		res[i] = n
	}
	return res
}

func (r *cookbook[D, I, O]) releaseLimit() {
	atomic.AddInt32(r.running, -1)
	r.runningLock.Lock()
	_ = r.spinLocker.TryLock()
	r.spinLocker.Unlock()
	r.runningLock.Unlock()
}
