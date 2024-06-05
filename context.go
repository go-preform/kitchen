package kitchen

import (
	"context"
)

// Context is a struct that holds various information and dependencies needed for the dishes.
type Context[D ICookware] struct {
	context.Context
	menu         iMenu[D]
	sets         []iSet[D]
	dish         iDish[D]
	session      []IDishServe
	sideEffects  []IInstance
	cookware     D
	traceableDep ITraceableCookware[D]
	inherited    IContextWithSession
	node         IDishServe
	tracerSpan   iTraceSpan[D]
	webContext   *webContext
}

type webContext struct {
	context.Context
	cookware     ICookware
	hasServedWeb bool
	ch           <-chan struct{}
	err          error
	bundle       IWebBundle
}

// NewWebContext creates a new web context for web router wrappers and prevent the dish context ends after the web request ends.
func NewWebContext(ctx context.Context, bundle IWebBundle, cookware ICookware) *webContext {
	wc := &webContext{Context: ctx, bundle: bundle, cookware: cookware}
	wc.ch = ctx.Done()
	return wc
}

// servedWeb is called when the web request has been served, ends the context from web request.
func (c *webContext) servedWeb() {
	c.hasServedWeb = true
	c.err = c.Context.Err()
	c.ch = make(chan struct{})
}

func (c webContext) Done() <-chan struct{} {
	return c.ch
}

func (c *webContext) Err() error {
	if c.hasServedWeb {
		return c.err
	}
	return c.Context.Err()
}

func (c *Context[D]) SetCtx(ctx context.Context) {
	c.Context = ctx
}

func (c Context[D]) Menu() iMenu[D] {
	return c.menu
}

func (c Context[D]) Sets() []iSet[D] {
	return c.sets
}

func (c Context[D]) Dish() iDish[D] {
	return c.dish
}

func (c Context[D]) Dependency() D {
	return c.cookware
}

func (c Context[D]) RawCookware() ICookware {
	return c.cookware
}

func (c Context[D]) Cookware() D {
	return c.cookware
}

func (c Context[D]) traceableCookware() ITraceableCookware[D] {
	return c.traceableDep
}

// WebBundle returns the web bundle of the context, generated from router wrapper.
func (c Context[D]) WebBundle() IWebBundle {
	if c.webContext != nil {
		return c.webContext.bundle
	}
	return nil
}

func (c Context[D]) GetCtx() context.Context {
	return c.Context
}

func (c *Context[D]) startTrace(id string, input any) iTraceSpan[D] {
	c.Context = context.WithValue(c.Context, "kitchenDishId", id)
	if c.traceableDep != nil {
		var ctx context.Context
		ctx, c.tracerSpan = c.traceableDep.StartTrace(c, id, input)
		if ctx != c.Context {
			c.Context = ctx
		}

		if c.webContext != nil {
			body, err := c.webContext.bundle.Body()
			if err == nil && len(body) != 0 {
				c.tracerSpan.SetAttributes("webReqBody", string(body))
			}
		}
		return c.tracerSpan
	}
	return nil
}

func (c *Context[D]) logSideEffect(instanceName string, toLog []any) (IContext[D], iTraceSpan[D]) {
	if c.tracerSpan != nil {
		var (
			cc  = *c
			ccc = &cc
		)
		ccc.Context, ccc.tracerSpan = ccc.tracerSpan.logSideEffect(c, instanceName, toLog)
		return ccc, ccc.tracerSpan
	}
	return c, nil
}

// Session Context will pass through nesting call of dishes,
// this appends sessions and returns all the session of the context.
func (c *Context[D]) Session(nodes ...IDishServe) []IDishServe {
	if len(nodes) != 0 {
		if c.inherited != nil {
			c.node = nodes[0]
			return c.inherited.Session(nodes...)
		}
		c.session = nodes
	} else {
		if c.inherited != nil {
			return c.inherited.Session()
		}
	}
	return c.session
}

// TraceSpan returns the trace span of the context, maybe nil if not the cookware not implement any tracer.
func (c *Context[D]) TraceSpan() iTraceSpan[D] {
	return c.tracerSpan
}

// servedWeb is called when the web request has been served, ends the context from web request.
func (c *Context[D]) servedWeb() {
	if c.webContext != nil {
		c.webContext.servedWeb()
	}
}

// served is called when the dish has been served, clean up.
func (c *Context[D]) served() {
	if len(c.session) == 1 {
		c.dish.menu().cookwareRecycle(c.cookware)
	}
}

func (c *Context[D]) setCookware(cw D) {
	c.cookware = cw
}

type PipelineContext[D IPipelineCookware[M], M IPipelineModel] struct {
	Context[D]
	tx IDbTx
}

func (b PipelineContext[D, M]) Tx() IDbTx {
	return b.tx
}

func (b PipelineContext[D, M]) Pipeline() iPipeline[D, M] {
	return b.menu.(iPipeline[D, M])
}

func (b PipelineContext[D, M]) Stage() iPipelineStage[D, M] {
	return b.sets[0].(iPipelineStage[D, M])
}

func (c *PipelineContext[D, M]) logSideEffect(instanceName string, toLog []any) (IContext[D], iTraceSpan[D]) {
	c.Context.logSideEffect(instanceName, toLog)
	return c, nil
}
