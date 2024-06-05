package kitchen

import (
	"context"
)

// A ContextForTest is for mocking the Context struct for testing.
type ContextForTest[D ICookware] struct {
	context.Context
	SessionServed []IDishServe
	DummyCookware D
	DummyDish     iDish[D]
	DummySets     []iSet[D]
	DummyMenu     iMenu[D]
	webBundle     IWebBundle
}

func (c *ContextForTest[D]) Session(serve ...IDishServe) []IDishServe {
	c.SessionServed = append(c.SessionServed, serve...)
	return c.SessionServed
}

func (c ContextForTest[D]) SetCtx(ctx context.Context) {
	c.Context = ctx
}

func (c ContextForTest[D]) RawCookware() ICookware {
	return c.DummyCookware
}

func (c ContextForTest[D]) Menu() iMenu[D] {
	return c.DummyMenu
}

func (c ContextForTest[D]) Sets() []iSet[D] {
	return c.DummySets
}

func (c ContextForTest[D]) Dish() iDish[D] {
	return c.DummyDish
}

func (c ContextForTest[D]) Dependency() D {
	return c.DummyCookware
}

func (c ContextForTest[D]) Cookware() D {
	return c.DummyCookware
}

func (c ContextForTest[D]) traceableCookware() ITraceableCookware[D] {
	return nil
}

func (c ContextForTest[D]) startTrace(id string, input any) iTraceSpan[D] {
	return nil
}

func (c ContextForTest[D]) logSideEffect(instanceName string, toLog []any) (IContext[D], iTraceSpan[D]) {
	return nil, nil
}

func (c ContextForTest[D]) WebBundle() IWebBundle {
	return c.webBundle
}

func (c *ContextForTest[D]) SetWebBundle(body []byte, bundle IWebBundle) {
	c.webBundle = bundle
}

func (c ContextForTest[D]) TraceSpan() iTraceSpan[D] {
	return nil
}

func (c ContextForTest[D]) GetCtx() context.Context {
	return c.Context
}

func (c *ContextForTest[D]) servedWeb() {
}

func (c *ContextForTest[D]) served() {}

func (c *ContextForTest[D]) setCookware(cookware D) {
	c.DummyCookware = cookware
}

type PipelineContextForTest[D IPipelineCookware[M], M IPipelineModel] struct {
	ContextForTest[D]
	DummyTx IDbTx
}

func (b PipelineContextForTest[D, M]) Tx() IDbTx {
	return b.DummyTx
}

func (b PipelineContextForTest[D, M]) Pipeline() iPipeline[D, M] {
	return b.DummyMenu.(iPipeline[D, M])
}

func (b PipelineContextForTest[D, M]) Stage() iPipelineStage[D, M] {
	return b.DummySets[0].(iPipelineStage[D, M])
}

func (c *PipelineContextForTest[D, M]) logSideEffect(instanceName string, toLog []any) (IContext[D], iTraceSpan[D]) {
	return c, nil
}
