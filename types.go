package kitchen

import (
	"context"
	"database/sql"
	"github.com/go-preform/kitchen/delivery"
	"net/http"
	"net/url"
	"reflect"
)

type (
	DishCooker[D ICookware, I any, O any] func(IContext[D], I) (output O, err error)

	PipelineDishCooker[D IPipelineCookware[M], M IPipelineModel, I any, O any] func(IPipelineContext[D, M], M, I) (output O, toSaveCanNil M, err error)

	AfterListenHandlers[D ICookware, I any, O any] func(ctx IContext[D], input I, output O, err error)

	iMarshaller[I any] interface {
		unmarshal(raw []byte) (output I, recycle func(any), err error)
		marshal(output I) (outputData []byte, err error)
	}
	IWebBundle interface {
		Ctx() context.Context
		Method() string
		Body() ([]byte, error)
		Url() *url.URL
		UrlParams() map[string]string
		Headers() http.Header
		Raw() any
		Response() http.ResponseWriter
	}
	ICookware interface {
	}
	ICookwareInheritable interface {
		ICookware
		Inherit(ICookware) ICookware
	}
	ICookwareFactory[D ICookware] interface {
		New() D
		Put(any)
	}
	ITraceableCookware[D ICookware] interface {
		StartTrace(ctx IContext[D], id string, input any) (context.Context, iTraceSpan[D])
	}
	ITraceSpan interface {
		Detail() any
		End(output any, err error)
		AddEvent(name string, attrSets ...map[string]any)
		SetAttributes(key string, value any)
		Raw() any
	}
	iTraceSpan[D ICookware] interface {
		ITraceSpan
		logSideEffect(ctx IContext[D], instanceName string, toLog []any) (context.Context, iTraceSpan[D])
	}
	IInstance interface {
		Name() string
		Menu() IMenu
		Nodes() []IInstance
	}
	iCookbook[D ICookware] interface {
		IInstance
		ifLock() func()
		inherit(...iCookbook[D])
		emitAfterCook(IContext[D], any, any, error)
		isTraceableDep() bool
		isInheritableDep() bool
		menu() iMenu[D]
	}
	IKitchen interface {
		Order(dish IDish, input any) (output interface{}, err error)
		canHandle(uint32, int32) bool
	}
	IManager interface {
		AddMenu(menuInitializer func() IMenu) IManager
		SetMainKitchen(url string, port uint16) IManager
		Order(dish IDish) (func(ctx context.Context, input []byte) (output []byte, err error), error)
		Init() (IManager, error)
		SelectServeMenus(menuNames ...string) IManager
		DisableMenu(name string) IManager
	}
	IMenu interface {
		IInstance
		Name() string
		Manager() IManager
		Cookware() ICookware
		setManager(m IManager, id uint32)
		ID() uint32
		orderDish(context.Context, *delivery.Order)
		cookwareRecycle(any)
	}
	iMenu[D ICookware] interface {
		IMenu
		iCookbook[D]
		init(iMenu[D], any)
		initWithoutFields(iMenu[D], any)
		setName(name string)
		setCookware(any)
		cookware() D
		Dependency() D
		pushDish(iDish[D]) int
		Dishes() []iDish[D]
	}
	ISet interface {
		IInstance
		Name() string
		Menu() IMenu
		Tree() []ISet
	}
	iSet[D ICookware] interface {
		ISet
		iCookbook[D]
		init(menu iMenu[D], group iSet[D], parent iSet[D], name string)
		tree() []iSet[D]
		menu() iMenu[D]
	}
	IDish interface {
		Name() string
		FullName() string
		Input() any
		IO() (any, any)
		Id() uint32
		Menu() IMenu
		Cookware() ICookware
		Sets() []ISet
		Tags() reflect.StructTag
		cookByte(ctx context.Context, inputData []byte) (outputData []byte, err error)
		CookAny(ctx context.Context, input any) (output any, err error)
	}
	iDish[D ICookware] interface {
		IDish
		iCookbook[D]
		init(iCookbook[D], iDish[D], string, reflect.StructTag)
		refreshCooker()
	}
	IContextWithSession interface {
		context.Context
		Session(...IDishServe) []IDishServe
		SetCtx(context.Context)
		RawCookware() ICookware
		FromWeb() IWebBundle
		GetCtx() context.Context
		servedWeb()
		served()
	}
	IDishServe interface {
		finish(output any, err error)
		Record() (action IDish, finish bool, input any, output any, err error)
	}
	IContext[D ICookware] interface {
		IContextWithSession
		traceableCookware() ITraceableCookware[D]
		startTrace(id string, input any) iTraceSpan[D]
		Menu() iMenu[D]
		Sets() []iSet[D]
		Dish() iDish[D]
		Dependency() D
		Cookware() D
		logSideEffect(instanceName string, toLog []any) (IContext[D], iTraceSpan[D])
		TraceSpan() iTraceSpan[D]
	}
	IPipelineContext[D IPipelineCookware[M], M IPipelineModel] interface {
		IContext[D]
		Tx() IDbTx
		Pipeline() iPipeline[D, M]
		Stage() iPipelineStage[D, M]
	}
	PipelineStatus string
	IPipelineModel interface {
		GetStatus() PipelineStatus
		SetStatus(status PipelineStatus)
		PrimaryKey() any
	}
	IPipelineModelCanMap interface {
		ToMap() map[string]string
	}
	IDbRunner interface {
		QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
		PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
		ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
		QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	}
	IDbTx interface {
		IDbRunner
		Rollback() error
		Commit() error
	}
	IPipelineCookware[M IPipelineModel] interface {
		ICookware
		BeginTx(context.Context, ...*sql.TxOptions) (IDbTx, error)
		FinishTx(IDbTx, error) error
		GetModelById(ctx context.Context, pks ...any) (M, error)
		SaveModel(db IDbRunner, model M, oStatus PipelineStatus) error
	}
	IPipeline interface {
		IMenu
		GetActionsForModel(any) (status string, actions []IPipelineAction)
		GetActionsForStatus(status string) []IPipelineAction
		NewModel() IPipelineModel
	}
	iPipeline[D IPipelineCookware[M], M IPipelineModel] interface {
		IPipeline
		iMenu[D]
		initPipeline(pipeline iPipeline[D, M], bundle D)
	}
	IPipelineStage interface {
		ISet
		Status() PipelineStatus
		Actions() []IPipelineAction
	}
	iPipelineStage[D IPipelineCookware[M], M IPipelineModel] interface {
		IPipelineStage
		iSet[D]
		initStage(parent iCookbook[D], stage iPipelineStage[D, M], stageName PipelineStatus)
		pipeline() iPipeline[D, M]
	}
	IPipelineAction interface {
		IDish
		Status() PipelineStatus
		ModelToMap(model IPipelineModel) map[string]string
		ExecByIdAny(ctx context.Context, input any, ids ...any) (output any, err error)
		WillCreateModel() bool
		Pipeline() IPipeline
	}
	iPipelineAction[D IPipelineCookware[M], M IPipelineModel] interface {
		IPipelineAction
		iDish[D]
		initAction(parent iCookbook[D], act iPipelineAction[D, M], name string, tags reflect.StructTag)
	}
)
