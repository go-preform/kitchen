package kitchen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-preform/kitchen/delivery"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
)

type Dish[D ICookware, I any, O any] struct {
	cookbook[D, I, O]
	sets          []iSet[D]
	_menu         iMenu[D]
	name          string
	cooker        DishCooker[D, I, O]
	rawCooker     DishCooker[D, I, O]
	newInput      func() I
	asyncChan     chan asyncTask[D, I, O]
	path          *string
	operationId   string
	fieldTags     reflect.StructTag
	marshalInput  iMarshaller[I]
	marshalOutput iMarshaller[O]
	id            uint32
	panicRecover  bool
}

type asyncTask[D ICookware, I, O any] struct {
	ctx      IContext[D]
	input    I
	callback func(O, error)
}

func initDish[D ICookware](parent iCookbook[D], action iDish[D], name string, tags reflect.StructTag) {
	action.init(parent, action, name, tags)
}

func (a *Dish[D, I, O]) extractTags(tags reflect.StructTag, key string, fn func(string) *Dish[D, I, O]) {
	fn(tags.Get(key))
}

func (a *Dish[D, I, O]) init(parent iCookbook[D], action iDish[D], name string, tags reflect.StructTag) {
	a.cookbook.init()
	a.concurrentLimit = new(int32)
	a.running = new(int32)
	a.spinLocker = &sync.Mutex{}
	a.runningLock = &sync.Mutex{}
	a.name = name
	a.fieldTags = tags
	a.instance = action
	if set, ok := any(parent).(iSet[D]); ok {
		var setNames []string
		a.sets = set.tree()
		for _, g := range a.sets {
			a.inherit(g)
			setNames = append([]string{g.Name()}, setNames...)
		}
		a.inherit(set.menu())
		a._menu = set.menu()
		if len(setNames) != 0 {
			a.fullName = fmt.Sprintf("%s.%s.%s", a._menu.Name(), strings.Join(setNames, "."), a.name)
		} else {
			a.fullName = fmt.Sprintf("%s.%s", a._menu.Name(), a.name)
		}
	} else {
		a.inherit(parent)
		a._menu = any(parent).(iMenu[D])
		a.fullName = fmt.Sprintf("%s.%s", a._menu.Name(), a.name)
	}
	var (
		input  I
		output O
	)
	if _, ok := any(input).(proto.Message); ok {
		a.marshalInput = newProtobufMarshaler(input)
	} else {
		a.marshalInput = newJsonUnmarshaler(input)
	}
	if _, ok := any(output).(proto.Message); ok {
		a.marshalOutput = newProtobufMarshaler(output)
	} else {
		a.marshalOutput = newJsonUnmarshaler(output)
	}
	a.id = uint32(a._menu.pushDish(action))
	a.isTraceable = a._menu.isTraceableDep()
	var (
		iType = reflect.TypeOf((*I)(nil)).Elem()
	)
	if iType.Kind() == reflect.Ptr {
		a.newInput = func() I {
			return reflect.New(iType.Elem()).Interface().(I)
		}
	} else {
		a.newInput = func() I {
			var i I
			return i
		}
	}
}

type jsonMarshaller[I any] struct {
	pool sync.Pool
}

func (a *jsonMarshaller[I]) marshal(input I) ([]byte, error) {
	return json.Marshal(input)
}

func (a *jsonMarshaller[I]) unmarshal(input []byte) (I, func(any), error) {
	var (
		err error
	)
	msg := a.pool.Get().(I)
	err = json.Unmarshal(input, &msg)
	return msg, a.pool.Put, err
}

func newJsonUnmarshaler[I any](input I) iMarshaller[I] {
	inputType := reflect.TypeOf(input)
	if inputType != nil && inputType.Kind() == reflect.Ptr {
		inputType = inputType.Elem()
	}
	return &jsonMarshaller[I]{
		pool: sync.Pool{
			New: func() any {
				return reflect.New(inputType).Interface()
			},
		},
	}
}

type protobufMarshaller[I any] struct {
	pool sync.Pool
}

func (a *protobufMarshaller[I]) marshal(input I) ([]byte, error) {
	return proto.Marshal(any(input).(proto.Message))
}

func (a *protobufMarshaller[I]) unmarshal(input []byte) (I, func(any), error) {
	var (
		err error
	)
	msg := a.pool.Get().(I)
	err = proto.Unmarshal(input, any(msg).(proto.Message))
	return msg, a.pool.Put, err
}

func newProtobufMarshaler[I any](input I) iMarshaller[I] {
	inputType := reflect.TypeOf(input).Elem()
	return &protobufMarshaller[I]{
		pool: sync.Pool{
			New: func() any {
				return reflect.New(inputType).Interface()
			},
		},
	}
}

func (a Dish[D, I, O]) Id() uint32 {
	return a.id
}

func (a Dish[D, I, O]) Input() any {
	return a.newInput()
}

func (a Dish[D, I, O]) IO() (any, any) {
	var (
		o O
	)
	return a.newInput(), o
}

func (a Dish[D, I, O]) Name() string {
	if a.path != nil {
		return *a.path
	}
	return a.name
}

func (a Dish[D, I, O]) FullName() string {
	return a.fullName
}

func (a Dish[D, I, O]) Tags() reflect.StructTag {
	return a.fieldTags
}

func (a Dish[D, I, O]) menu() iMenu[D] {
	return a._menu
}

func (a Dish[D, I, O]) Menu() IMenu {
	return a._menu
}

func (a Dish[D, I, O]) Sets() []ISet {
	var res = make([]ISet, len(a.sets))
	for i, s := range a.sets {
		res[i] = s
	}
	return res
}

func (a *Dish[D, I, O]) SetAsyncCooker(ctx context.Context, buffSize, threadSize int, cooker DishCooker[D, I, O]) {
	if a.asyncChan != nil {
		close(a.asyncChan)
		a.asyncChan = nil
	}
	if cooker != nil && threadSize > 0 && buffSize > 0 {
		a.asyncChan = make(chan asyncTask[D, I, O], buffSize)
		for i := 0; i < threadSize; i++ {
			go func() {
				var (
					err    error
					node   IDishServe
					t      asyncTask[D, I, O]
					ok     bool
					ch     = a.asyncChan
					output O
				)
				if a.panicRecover {
					defer func() {
						if rec := recover(); rec != nil {
							if a.isTraceable && t.ctx != nil && t.ctx.TraceSpan() != nil {
								t.ctx.TraceSpan().AddEvent("panic", map[string]any{"panic": rec, "stack": string(debug.Stack())})
							} else {
								fmt.Printf("panicRecover from panic: \n%v\n%s", rec, string(debug.Stack()))
							}
						}
					}()
				}
				for {
					select {
					case <-ctx.Done():
						close(ch)
						a.asyncChan = nil
					case t, ok = <-ch:
						if !ok {
							return
						}
						node = a.start(t.ctx, t.input, false)
						output, err = cooker(t.ctx, t.input)
						a.emitAfterCook(t.ctx, t.input, output, err)
						node.finish(nil, err)
						if t.callback != nil {
							t.callback(output, err)
						}
					}
				}
			}()
		}
	}
}
func (a *Dish[D, I, O]) SetAsyncExecer(ctx context.Context, buffSize, threadSize int, cooker DishCooker[D, I, O]) {
	a.SetAsyncCooker(ctx, buffSize, threadSize, cooker)
}
func (a *Dish[D, I, O]) SetExecer(cooker DishCooker[D, I, O]) *Dish[D, I, O] {
	return a.SetCooker(cooker)
}

func (a *Dish[D, I, O]) refreshCooker() {
	if a.rawCooker != nil {
		a.SetCooker(a.rawCooker)
	}
}

func (a *Dish[D, I, O]) SetCooker(cooker DishCooker[D, I, O]) *Dish[D, I, O] {
	if a.asyncChan != nil {
		close(a.asyncChan)
		a.asyncChan = nil
	}
	a.rawCooker = cooker
	if mgr := a._menu.Manager(); mgr != nil {
		a.cooker = func(ctx IContext[D], input I) (output O, err error) {
			var (
				handler func(ctx context.Context, input []byte) (output []byte, err error)
			)
			handler, err = mgr.Order(a)
			if errors.Is(err, delivery.ErrRunInLocal) {
				return cooker(ctx, input)
			} else if err != nil {
				return
			}
			var (
				orderOutput []byte
				orderInput  []byte
			)
			orderInput, err = a.marshalInput.marshal(input)
			if err != nil {
				return
			}
			orderOutput, err = handler(ctx, orderInput)
			if err != nil {
				return
			}
			output, _, err = a.marshalOutput.unmarshal(orderOutput)
			return
		}
	} else {
		a.cooker = cooker
	}
	return a
}

func (a *Dish[D, I, O]) PanicRecover(recover bool) *Dish[D, I, O] {
	a.panicRecover = recover
	return a
}

func (a Dish[D, I, O]) Cookware() ICookware {
	return a._menu.Cookware()
}

func (a Dish[D, I, O]) cookware() D {
	d := a._menu.cookware()
	return d
}

func (a Dish[D, I, O]) Dependency() D {
	d := a._menu.cookware()
	return d
}

var ErrCookerNotSet = errors.New("cooker not set")

func (a *Dish[D, I, O]) Cook(ctx context.Context, input I) (output O, err error) {
	if a.asyncChan != nil {
		l := &sync.Mutex{}
		l.Lock()
		err = a.CookAsync(ctx, input, func(o O, e error) {
			output = o
			err = e
			l.Unlock()
		})
		if err != nil {
			l.Unlock()
			return
		}
		l.Lock()
		return
	}
	return a.cook(a.newCtx(ctx), input, nil)
}

func (a *Dish[D, I, O]) cookByte(ctx context.Context, inputData []byte) (outputData []byte, err error) {
	input, recycle, err := a.marshalInput.unmarshal(inputData)
	if err != nil {
		return nil, err
	}
	output, err := a.doCook(a.rawCooker, a.newCtx(ctx), input, nil)
	recycle(input)
	if err != nil {
		return nil, err
	}
	return a.marshalOutput.marshal(output)
}

// deprecated use CookWithCookware
func (a *Dish[D, I, O]) ExecWithDep(ctx context.Context, dep D, input I) (output O, err error) {
	return a.CookWithCookware(ctx, dep, input)
}

func (a *Dish[D, I, O]) CookWithCookware(ctx context.Context, cookware D, input I) (output O, err error) {
	return a.Cook(a.newCtx(ctx, cookware), input)
}

// deprecated use Cook
func (a *Dish[D, I, O]) Exec(ctx context.Context, input I) (output O, err error) {
	return a.Cook(ctx, input)
}

// deprecated use CookAsync
func (a *Dish[D, I, O]) ExecAsync(ctx context.Context, input I, optionalCallback ...func(O, error)) error {
	return a.CookAsync(ctx, input, optionalCallback...)
}

func (a *Dish[D, I, O]) CookAsync(ctx context.Context, input I, optionalCallback ...func(O, error)) error {
	if a.asyncChan == nil {
		return errors.New("async cooker not set")
	}
	var cb func(O, error)
	if len(optionalCallback) != 0 {
		cb = optionalCallback[0]
	}
	a.asyncChan <- asyncTask[D, I, O]{ctx: a.newCtx(ctx), input: input, callback: cb}
	return nil
}

func (a *Dish[D, I, O]) doCook(cooker DishCooker[D, I, O], ctx IContext[D], input I, followUp func(O, error) error) (output O, err error) {
	node := a.start(ctx, input, a.panicRecover)
	if cooker == nil {
		err = ErrCookerNotSet
	} else {
		output, err = cooker(ctx, input)
		if followUp != nil {
			err = followUp(output, err)
		}
	}
	a.emitAfterCook(ctx, input, output, err)
	node.finish(output, err)
	return output, err
}

func (a *Dish[D, I, O]) cook(ctx IContext[D], input I, followUp func(O, error) error) (output O, err error) {
	return a.doCook(a.cooker, ctx, input, followUp)
}

var nilnil any

func (a *Dish[D, I, O]) CookAny(ctx context.Context, input any) (output any, err error) {
	if input == nilnil {
		var i I
		out, err := a.Cook(ctx, i)
		return out, err
	}
	out, err := a.Cook(ctx, input.(I))
	return out, err
}

func (a *Dish[D, I, O]) newCtx(ctx context.Context, cookware ...D) *Context[D] {
	var (
		c  *Context[D]
		ok bool
	)
	if c, ok = ctx.(*Context[D]); ok {
		cc := &Context[D]{Context: c, menu: a._menu, sets: a.sets, dish: a}
		cc.cookware = c.cookware
		cc.traceableDep = c.traceableDep
		return cc
	}
	c = &Context[D]{Context: ctx, menu: a._menu, sets: a.sets, dish: a}
	var (
		cw D
	)
	if len(cookware) != 0 {
		cw = cookware[0]
	} else {
		if c.webContext, ok = ctx.(*webContext); ok {
			cw = c.webContext.cookware.(D)
		} else {
			cw = a.cookware()
		}
	}
	if c.inherited, ok = ctx.(IContextWithSession); ok {
		if a._menu.isInheritableDep() {
			c.cookware = any(cw).(ICookwareInheritable).Inherit(c.inherited.RawCookware()).(D)
		} else {
			c.cookware = cw
		}
	} else {
		c.cookware = cw
	}
	if a.isTraceable {
		c.traceableDep = any(c.cookware).(ITraceableCookware[D])
	}
	return c
}

func (a Dish[D, I, O]) start(ctx IContext[D], input I, panicRecover bool) (sess *dishServing) {
	sess = a.newServing(input) //&dishServing{ctx: ctx, Input: input}
	if a.isTraceable {
		sess.tracerSpan = ctx.startTrace(TraceIdGenerator(), input)
	}
	if len(ctx.Session(sess)) == 1 {
		sess.unlocker = a.ifLockThis()
		if panicRecover {
			defer func() {
				if rec := recover(); rec != nil {
					if a.isTraceable {
						sess.tracerSpan.AddEvent("panic", map[string]any{"panic": rec, "stack": string(debug.Stack())})
					} else {
						fmt.Printf("panicRecover from panic: \n%v\n%s", a, string(debug.Stack()))
					}
					sess.finish(nil, fmt.Errorf("panic: %v", rec))
				}
			}()
		}
	}
	return
}
func (r *Dish[D, I, O]) newServing(input I) *dishServing {
	serving := &dishServing{}
	serving.Action = r.instance.(IDish)
	serving.Input = input
	return serving
}

type dishServing struct {
	Action     IDish
	Input      any
	Output     any
	Error      error
	Finish     bool
	unlocker   func()
	tracerSpan ITraceSpan
}

func (node *dishServing) finish(output any, err error) {
	if node.unlocker != nil {
		node.unlocker()
	}
	if node.tracerSpan != nil {
		node.tracerSpan.End(output, err)
	}
	node.Finish = true
	node.Output = output
	node.Error = err
}

func (node *dishServing) Record() (IDish, bool, any, any, error) {
	return node.Action, node.Finish, node.Input, node.Output, node.Error
}
