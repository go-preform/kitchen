package kitchen

import (
	"context"
	"fmt"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	TraceIdGenerator = traceId
)

func traceId() string {
	return strconv.FormatInt(rand.Int63n(999999999), 36)
}

type ChainTraceableCookware[D ICookware] []ITraceableCookware[D]

func NewChainTraceableCookware[D ICookware](deps ...ITraceableCookware[D]) *ChainTraceableCookware[D] {
	var chain = ChainTraceableCookware[D](deps)
	return &chain
}

func (d ChainTraceableCookware[D]) StartTrace(ctx IContext[D], id string, input any) (context.Context, iTraceSpan[D]) {
	var (
		spans   = make([]iTraceSpan[D], len(d))
		span    iTraceSpan[D]
		thisCtx context.Context
	)
	for i, dep := range d {
		thisCtx, span = dep.StartTrace(ctx, id, input)
		spans[i] = span
		ctx.SetCtx(thisCtx)
	}
	return thisCtx, &chainTraceSpan[D]{spans: spans}
}

type chainTraceSpan[D ICookware] struct {
	spans []iTraceSpan[D]
}

func (c chainTraceSpan[D]) logSideEffect(ctx IContext[D], instanceName string, toLog []any) (context.Context, iTraceSpan[D]) {
	var (
		subSpan = &chainTraceSpan[D]{spans: make([]iTraceSpan[D], len(c.spans))}
		thisCtx context.Context
	)
	for i, span := range c.spans {
		thisCtx, subSpan.spans[i] = span.logSideEffect(ctx, instanceName, toLog)
		ctx.SetCtx(thisCtx)
	}
	return ctx, subSpan
}

func (c chainTraceSpan[D]) Detail() any {
	var details = make([]any, 0, len(c.spans))
	for i, span := range c.spans {
		details[i] = span.Detail()
	}
	return details
}

func (c chainTraceSpan[D]) End(output any, err error) {
	for _, span := range c.spans {
		span.End(output, err)
	}
}

func (c chainTraceSpan[D]) AddEvent(name string, attrSets ...map[string]any) {
	for _, span := range c.spans {
		span.AddEvent(name, attrSets...)
	}
}

func (c chainTraceSpan[D]) SetAttributes(key string, value any) {
	for _, span := range c.spans {
		span.SetAttributes(key, value)
	}
}

func (c chainTraceSpan[D]) Raw() any {
	var raws = make([]any, 0, len(c.spans))
	for _, span := range c.spans {
		raws = append(raws, span.Raw())
	}
	return raws
}

type CounterTraceableCookware[D ICookware] struct {
	Menus []*MenuCounter
	lock  sync.Mutex
}

type counterTraceSpan[D ICookware] struct {
	dishId  int
	menuCnt *MenuCounter
	parent  *CounterTraceableCookware[D]
}

type MenuCounter struct {
	lock           sync.Mutex
	Ok             *uint64
	Err            *uint64
	SideEffect     *uint64
	DishOk         []*uint64
	DishErr        []*uint64
	DishSideEffect []*uint64
}

func NewCounterTraceableCookware[D ICookware]() *CounterTraceableCookware[D] {
	return &CounterTraceableCookware[D]{}
}

func (d *CounterTraceableCookware[D]) StartTrace(ctx IContext[D], id string, input any) (context.Context, iTraceSpan[D]) {
	d.lock.Lock()
	if l, ll := len(d.Menus), int(ctx.Menu().ID()); l <= ll {
		for i := l; i <= ll; i++ {
			d.Menus = append(d.Menus, &MenuCounter{
				Ok:         new(uint64),
				Err:        new(uint64),
				SideEffect: new(uint64),
			})
		}
	}
	d.lock.Unlock()
	menuCnt := d.Menus[ctx.Menu().ID()]
	dishId := int(ctx.Dish().Id())
	menuCnt.lock.Lock()
	if l := len(menuCnt.DishOk); l <= dishId {
		for i := l; i <= dishId; i++ {
			menuCnt.DishOk = append(menuCnt.DishOk, new(uint64))
			menuCnt.DishErr = append(menuCnt.DishErr, new(uint64))
			menuCnt.DishSideEffect = append(menuCnt.DishSideEffect, new(uint64))
		}
	}
	menuCnt.lock.Unlock()
	return ctx, &counterTraceSpan[D]{
		menuCnt: menuCnt,
		dishId:  dishId,
		parent:  d,
	}
}

func (c *counterTraceSpan[D]) logSideEffect(ctx IContext[D], instanceName string, toLog []any) (context.Context, iTraceSpan[D]) {
	atomic.AddUint64(c.menuCnt.DishSideEffect[c.dishId], 1)
	atomic.AddUint64(c.menuCnt.SideEffect, 1)
	return ctx, &counterTraceSpan[D]{}
}

func (d counterTraceSpan[D]) Detail() any {
	return nil
}

func (d *counterTraceSpan[D]) End(output any, err error) {
	if d.menuCnt == nil {
		//side effect skip
		return
	}
	if err == nil {
		atomic.AddUint64(d.menuCnt.Ok, 1)
		atomic.AddUint64(d.menuCnt.DishOk[d.dishId], 1)
	} else {
		atomic.AddUint64(d.menuCnt.Err, 1)
		atomic.AddUint64(d.menuCnt.DishErr[d.dishId], 1)
	}
}

func (d counterTraceSpan[D]) AddEvent(name string, attrSets ...map[string]any) {
}

func (d counterTraceSpan[D]) SetAttributes(key string, value any) {
}

func (d counterTraceSpan[D]) Raw() any {
	return d.parent.Menus
}

type ZeroLogTraceableCookware[D ICookware] struct {
	Logger *zerolog.Logger
}

type zeroLogTraceSpan[D ICookware] struct {
	logger *zerolog.Logger
}

func NewZeroLogTraceableCookware[D ICookware](l *zerolog.Logger) *ZeroLogTraceableCookware[D] {
	return &ZeroLogTraceableCookware[D]{Logger: l}
}

func (d ZeroLogTraceableCookware[D]) StartTrace(ctx IContext[D], id string, input any) (context.Context, iTraceSpan[D]) {
	if d.Logger.GetLevel() > zerolog.DebugLevel {
		return ctx, &zeroLogTraceSpan[D]{d.Logger}
	}
	logger := d.Logger.With().Str("dishId", id).Str("action", ctx.Dish().FullName()).Logger()
	logger.Debug().Interface("input", input).Msg("call")
	return ctx.GetCtx(), &zeroLogTraceSpan[D]{logger: &logger}
}

func (c zeroLogTraceSpan[D]) logSideEffect(ctx IContext[D], instanceName string, toLog []any) (context.Context, iTraceSpan[D]) {
	if c.logger.GetLevel() > zerolog.DebugLevel {
		return ctx.GetCtx(), &zeroLogTraceSpan[D]{c.logger}
	}
	logger := c.logger.With().Str("sideEffect", instanceName).Logger()
	logger.Debug().Interface("desc", toLog).Msg("call")
	return ctx.GetCtx(), &zeroLogTraceSpan[D]{logger: &logger}
}

func (d zeroLogTraceSpan[D]) Detail() any {
	return nil
}

func (d zeroLogTraceSpan[D]) End(output any, err error) {
	d.logger.Debug().Str("output", fmt.Sprintf("%v", output)).Err(err).Msg("return")
}

func (d zeroLogTraceSpan[D]) AddEvent(name string, attrSets ...map[string]any) {
	d.logger.Debug().Interface("event", name).Interface("attrs", attrSets).Msg("event")
}

func (d zeroLogTraceSpan[D]) SetAttributes(key string, value any) {
	d.logger.Debug().Interface(key, value).Msg("attrs")
}

func (d zeroLogTraceSpan[D]) Raw() any {
	return d.logger
}

type otelTraceableCookware[D ICookware] struct {
	t trace.Tracer
}

func NewOtelTraceableCookware[D ICookware](t trace.Tracer) *otelTraceableCookware[D] {
	return &otelTraceableCookware[D]{t: t}
}

func (d otelTraceableCookware[D]) StartTrace(ctx IContext[D], id string, input any) (context.Context, iTraceSpan[D]) {
	thisCtx, span := d.t.Start(ctx, ctx.Dish().FullName(), trace.WithAttributes(attribute.String("dishId", id), attribute.String("input", fmt.Sprintf("%v", input))))
	return thisCtx, &otelTraceSpan[D]{span: span, t: d.t}
}

type otelTraceSpan[D ICookware] struct {
	span trace.Span
	t    trace.Tracer
}

func (o otelTraceSpan[D]) logSideEffect(ctx IContext[D], instanceName string, toLog []any) (context.Context, iTraceSpan[D]) {
	thisCtx, span := o.t.Start(ctx, "sideEffect:"+instanceName, trace.WithAttributes(attribute.String("desc", fmt.Sprintf("%v", toLog))))
	return thisCtx, &otelTraceSpan[D]{span: span, t: o.t}
}

func (o otelTraceSpan[D]) Detail() any {
	return o.span.SpanContext()
}

func (o otelTraceSpan[D]) End(output any, err error) {
	o.span.SetAttributes(attribute.String("output", fmt.Sprintf("%v", output)), attribute.String("error", fmt.Sprintf("%v", err)))
	o.span.End()
}

func (o *otelTraceSpan[D]) AddEvent(name string, attrSets ...map[string]any) {
	if len(attrSets) == 0 {
		o.span.AddEvent(name)
		return
	}
	o.span.AddEvent(name, trace.WithAttributes(o.makeAttr(attrSets...)...))
}

func (o otelTraceSpan[D]) SetAttributes(key string, value any) {
	o.span.SetAttributes(o.makeKv(key, value))
}

func (o otelTraceSpan[D]) Raw() any {
	return o.span
}

func (o otelTraceSpan[D]) makeAttr(attrSets ...map[string]any) []attribute.KeyValue {
	var kvs []attribute.KeyValue
	for _, attrs := range attrSets {
		for k, v := range attrs {
			kvs = append(kvs, o.makeKv(k, v))
		}
	}
	return kvs
}

func (o otelTraceSpan[D]) makeKv(k string, v any) attribute.KeyValue {
	switch v.(type) {
	case string:
		return attribute.String(k, v.(string))
	case uint:
		return attribute.Int(k, int(v.(uint)))
	case uint32:
		return attribute.Int64(k, int64(v.(uint32)))
	case uint64:
		return attribute.Int64(k, int64(v.(uint64)))
	case int:
		return attribute.Int(k, v.(int))
	case int32:
		return attribute.Int64(k, v.(int64))
	case int64:
		return attribute.Int64(k, v.(int64))
	case float64:
		return attribute.Float64(k, v.(float64))
	case bool:
		return attribute.Bool(k, v.(bool))
	default:
		return attribute.String(k, fmt.Sprintf("%v", v))
	}
}
