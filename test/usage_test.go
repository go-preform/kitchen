package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"github.com/go-preform/kitchen"
	helloworld "github.com/go-preform/kitchen/examples/proto"
	kitchenGrpc "github.com/go-preform/kitchen/grpc"
	kitchenWeb "github.com/go-preform/kitchen/web"
	"github.com/go-preform/kitchen/web/routerHelper"
	"github.com/go-preform/kitchen/web/routerHelper/muxHelper"
	vtgrpc "github.com/planetscale/vtprotobuf/codec/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

type Order struct {
	kitchen.MenuBase[*Order, DummyWorkerCookware]
	New struct {
		kitchen.SetBase[DummyWorkerCookware]
		Create kitchen.Dish[DummyWorkerCookware, int, int]
	}
	Pending struct {
		kitchen.SetBase[DummyWorkerCookware]
		List      kitchen.Dish[DummyWorkerCookware, *Filter, []*PurchaseOrder]
		TestA     kitchen.Dish[DummyWorkerCookware, int, int]
		TestGroup struct {
			TestB kitchen.Dish[DummyWorkerCookware, int, int]
		}
		TestAsync kitchen.Dish[DummyWorkerCookware, int, int]
	}
	Util struct {
		kitchen.SetBase[DummyWorkerCookware]
		TestC kitchen.Dish[DummyWorkerCookware, int, int]
	}
	TestWebUrl      kitchen.Dish[DummyWorkerCookware, any, any] `path:"notify/{order_id}" method:"POST"`
	TestNamedMethod struct {
		kitchen.SetBase[DummyWorkerCookware]
		POST kitchen.Dish[DummyWorkerCookware, any, any] `urlParam:"test"`
	}
}
type OrderWithTracer struct {
	kitchen.MenuBase[*OrderWithTracer, DummyWorkerCookwareWithTracer]
	New struct {
		kitchen.SetBase[DummyWorkerCookwareWithTracer]
		Create kitchen.Dish[DummyWorkerCookwareWithTracer, int, int]
	}
	Pending struct {
		kitchen.SetBase[DummyWorkerCookwareWithTracer]
		List      kitchen.Dish[DummyWorkerCookwareWithTracer, *Filter, []*PurchaseOrder]
		TestA     kitchen.Dish[DummyWorkerCookwareWithTracer, int, int]
		TestGroup struct {
			TestB kitchen.Dish[DummyWorkerCookwareWithTracer, int, int]
		}
		TestAsync kitchen.Dish[DummyWorkerCookwareWithTracer, int, int]
	}
	Util struct {
		kitchen.SetBase[DummyWorkerCookwareWithTracer]
		TestC kitchen.Dish[DummyWorkerCookwareWithTracer, int, int]
	}
	TestWebUrl      kitchen.Dish[DummyWorkerCookwareWithTracer, any, any] `path:"notify/{order_id}" method:"POST"`
	TestNamedMethod struct {
		kitchen.SetBase[DummyWorkerCookwareWithTracer]
		POST kitchen.Dish[DummyWorkerCookwareWithTracer, any, any] `urlParams:"test" urlParamDescs:"testDesc"`
	}
}

var (
	logger = zerolog.New(os.Stdout) //.With().Timestamp().Logger()
)

func TestInit(t *testing.T) {

	var (
		callStack     []int
		counterTracer = kitchen.NewCounterTraceableCookware[DummyWorkerCookwareWithTracer]()
	)

	OrderWorker := kitchen.InitMenu[*OrderWithTracer, DummyWorkerCookwareWithTracer](&OrderWithTracer{}, DummyWorkerCookwareWithTracer{
		ITraceableCookware: kitchen.ChainTraceableCookware[DummyWorkerCookwareWithTracer]{
			kitchen.NewZeroLogTraceableCookware[DummyWorkerCookwareWithTracer](&logger),
			counterTracer,
		},
	})
	OrderWorker.ConcurrentLimit(1) //only single thread
	OrderWorker.Pending.TestA.SetCooker(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input int) (output int, err error) {
		res, err := OrderWorker.Util.TestC.Exec(ctx, input)
		return res + 1, err
	})
	OrderWorker.Util.TestC.SetCooker(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input int) (output int, err error) {
		//ctx.Bundle().Sql.Query("select 1")
		return input + 1, nil
	})
	cnt := 0
	OrderWorker.Pending.TestAsync.SetCooker(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input int) (output int, err error) {
		cnt++
		time.Sleep(time.Millisecond * 100)
		return input + 999, nil
	})
	OrderWorker.Pending.TestAsync.Prefork(context.Background(), 1, 100)

	//should emit to MQ broadcast to whole cluster later
	OrderWorker.Pending.TestA.AfterCook(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input int, output int, err error) {
		callStack = append(callStack, 1)
	},
		"tell log about this callback...", //desc for log
	)
	OrderWorker.Pending.AfterCook(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input, output any, err error) {
		callStack = append(callStack, 2)
	})
	OrderWorker.AfterCook(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input, output any, err error) {
		callStack = append(callStack, 3)
	})
	OrderWorker.Pending.TestGroup.TestB.SetCooker(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input int) (output int, err error) {
		//ctx.Bundle().Sql.Query("select 1")
		return input + 1, nil
	})
	input := 1
	//if traffic growth up, we can have multiple instance and implement grpc & load balance inside Exec
	res, err := OrderWorker.Pending.TestA.Cook(context.Background(), input)
	assert.Equal(t, []int{3, 1, 2, 3}, callStack) //triggered all 3 events, and order again since we nested call order.Util.TestC
	assert.Equal(t, input+2, res)
	assert.Nil(t, err)

	res, err = OrderWorker.Pending.TestGroup.TestB.Cook(context.Background(), res)
	assert.Nil(t, err)
	assert.Equal(t, input+3, res)
	assert.Equal(t, []int{3, 1, 2, 3, 2, 3}, callStack) //triggered all again except order.Exist.TestA

	OrderWorker.Pending.TestAsync.CookAsync(context.Background(), 1, func(i int, err error) {
		assert.Equal(t, 1000, i)
		assert.Nil(t, err)
	}) //run as async
	OrderWorker.Pending.TestAsync.CookAsync(context.Background(), 1) //run as async should be blocking because of Prefork
	time.Sleep(time.Millisecond * 50)
	assert.Equal(t, 1, cnt)
	time.Sleep(time.Millisecond * 100)
	assert.Equal(t, 2, cnt)
	res, err = OrderWorker.Pending.TestAsync.Cook(context.Background(), 1) //run as sync
	assert.Nil(t, err)
	assert.Equal(t, 1000, res)

	assert.Equal(t, uint64(6), *counterTracer.Menus[0].Ok)

	kitchen.GroupPrefork(context.Background(), 1, 100, &OrderWorker.Pending.TestAsync, &OrderWorker.Pending.TestA)

	t1 := time.Now()
	OrderWorker.Pending.TestAsync.Cook(context.Background(), 1)
	OrderWorker.Pending.TestA.Cook(context.Background(), 1)
	assert.True(t, time.Since(t1) > time.Millisecond*100) //blocking by TestAsync Prefork

}

func TestUrl(t *testing.T) {

	OrderWorker := kitchen.InitMenu[*OrderWithTracer, DummyWorkerCookwareWithTracer](&OrderWithTracer{}, DummyWorkerCookwareWithTracer{
		ITraceableCookware: kitchen.NewZeroLogTraceableCookware[DummyWorkerCookwareWithTracer](&logger),
	})

	url, urlParams, method, webUrlParamMap := routerHelper.DishUrlAndMethod(&OrderWorker.TestWebUrl, routerHelper.DefaultUrlParamWrapper)

	assert.Equal(t, "POST", method)
	assert.Equal(t, "notify/{order_id}", url)
	assert.Equal(t, 0, len(urlParams))
	assert.Equal(t, 0, len(webUrlParamMap))
	fmt.Println("1", url)

	url, urlParams, method, webUrlParamMap = routerHelper.DishUrlAndMethod(&OrderWorker.TestNamedMethod.POST, routerHelper.DefaultUrlParamWrapper)
	fmt.Println("2", url)
	assert.Equal(t, "POST", method)
	assert.Equal(t, 1, len(urlParams))
	assert.Equal(t, "{test}", urlParams[0][0])
	assert.Equal(t, "testDesc", urlParams[0][1])
	assert.Equal(t, 0, len(webUrlParamMap))
}

//func TestOtel(t *testing.T) {
//
//	type DummyTraceableCookware struct {
//		ITraceableCookware
//	}
//
//	type TestTrace struct {
//		kitchen.MenuBase[*TestTrace, *DummyTraceableCookware]
//		TestGroup1 struct {
//			kitchen.SetBase[*DummyTraceableCookware]
//			Test1      kitchen.Dish[*DummyTraceableCookware, int, int]
//			TestGroup2 struct {
//				kitchen.SetBase[*DummyTraceableCookware]
//				Test2 kitchen.Dish[*DummyTraceableCookware, int, int]
//			}
//		}
//	}
//
//	traceClient := otlptracegrpc.NewClient(
//		otlptracegrpc.WithInsecure(),
//		otlptracegrpc.WithEndpoint("127.0.0.1:4317"),
//		otlptracegrpc.WithDialOption(grpc.WithBlock()))
//	traceExp, traceExpErr := otlptrace.New(context.Background(), traceClient)
//	if traceExpErr != nil {
//		panic(traceExpErr)
//	}
//
//	res, resErr := resource.New(context.Background(),
//		resource.WithFromEnv(),
//		resource.WithProcess(),
//		resource.WithTelemetrySDK(),
//		resource.WithHost(),
//		resource.WithAttributes(
//			// the service name used to display traces in backends
//			semconv.ServiceNameKey.String("testTraceableWorker"),
//		),
//	)
//	if resErr != nil {
//		panic(resErr)
//	}
//
//	bsp := sdktrace.NewBatchSpanProcessor(traceExp, sdktrace.WithMaxQueueSize(6144))
//	tracerProvider := sdktrace.NewTracerProvider(
//		sdktrace.WithSampler(sdktrace.AlwaysSample()),
//		sdktrace.WithResource(res),
//		sdktrace.WithSpanProcessor(bsp),
//	)
//	otel.SetTracerProvider(tracerProvider)
//
//	TraceableWorker := kitchen.InitMenu[*TestTrace, *DummyTraceableCookware](&TestTrace{}, &DummyTraceableCookware{
//		ITraceableCookware: NewChainTraceableCookware(
//			kitchen.NewZeroLogTraceableCookware(&logger),
//			NewOtelTraceableCookware(otel.GetTracerProvider().Tracer(
//				"testTraceableWorker",
//			))),
//	})
//	TraceableWorker.TestGroup1.Test1.SetCooker(func(ctx kitchen.IContext[*DummyTraceableCookware], input int) (output int, err error) {
//		time.Sleep(100 * time.Millisecond)
//		input, err = TraceableWorker.TestGroup1.TestGroup2.Test2.Exec(ctx, input)
//		time.Sleep(100 * time.Millisecond)
//		return input + 1, err
//	})
//	TraceableWorker.TestGroup1.TestGroup2.Test2.SetCooker(func(ctx kitchen.IContext[*DummyTraceableCookware], input int) (output int, err error) {
//		time.Sleep(100 * time.Millisecond)
//		ctx.TraceSpan().AddEvent("test", map[string]interface{}{"input": input})
//		return input + 1, nil
//	})
//	TraceableWorker.TestGroup1.TestGroup2.Test2.AfterCook(func(ctx kitchen.IContext[*DummyTraceableCookware], input, output int, err error) {
//
//	})
//
//	res1, err := TraceableWorker.TestGroup1.Test1.Exec(context.Background(), 1)
//	assert.Nil(t, err)
//	assert.Equal(t, 3, res1)
//
//	//time.Sleep(time.Second * 5) //wait for grpc flush
//
//}

func TestPipeline(t *testing.T) {

	type OrderPipeline struct {
		kitchen.PipelineBase[*OrderPipeline, *DummyPipelineCookware, *PurchaseOrder]
		Create  kitchen.PipelineAction[*DummyPipelineCookware, *PurchaseOrder, any, any]
		Pending struct {
			kitchen.PipelineStage[*DummyPipelineCookware, *PurchaseOrder]
			SendRequest kitchen.PipelineAction[*DummyPipelineCookware, *PurchaseOrder, any, any]
		}
		WaitForCallback struct {
			kitchen.PipelineStage[*DummyPipelineCookware, *PurchaseOrder]
			Confirm kitchen.PipelineAction[*DummyPipelineCookware, *PurchaseOrder, any, any]
			Error   kitchen.PipelineAction[*DummyPipelineCookware, *PurchaseOrder, any, any]
		}
	}

	//psqlconn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", "127.0.0.1", 5432, "postgres", "postgres", "postgres")
	//// open database
	//conn, err := sql.Open("postgres", psqlconn)
	//assert.Nil(t, err)
	order := kitchen.InitPipeline[*DummyPipelineCookware, *PurchaseOrder, *OrderPipeline](&OrderPipeline{}, &DummyPipelineCookware{
		DummyWorkerCookwareWithTracer: DummyWorkerCookwareWithTracer{
			ITraceableCookware: kitchen.NewZeroLogTraceableCookware[DummyWorkerCookwareWithTracer](&logger),
		},
		PipelineSqlTxCookware: NewPipelineSqlTxCookware[*PurchaseOrder](&sql.DB{}),
	})
	assert.NotNil(t, order)
	order.Pending.SendRequest.SetCooker(func(i kitchen.IPipelineContext[*DummyPipelineCookware, *PurchaseOrder], order *PurchaseOrder, a any) (output any, toSaveCanNil *PurchaseOrder, err error) {
		fmt.Println("Pending.SendRequest")
		//ctx.Tx()
		return a, order, nil
	}).SetNextStage(order.WaitForCallback)

	model := &PurchaseOrder{Status: "Pending"}
	res, err := order.Pending.SendRequest.ExecWithModel(context.Background(), model, 1)
	assert.Nil(t, err)
	assert.Equal(t, 1, res)
	assert.Equal(t, order.WaitForCallback.Status(), model.Status)

	res, err = order.Pending.SendRequest.ExecById(context.Background(), 1, 1) //get from dep
	assert.Nil(t, err)
	assert.Equal(t, 1, res)
	assert.Equal(t, order.WaitForCallback.Status(), model.Status)

}

type WebTestWrapper struct {
	kitchen.MenuBase[*WebTestWrapper, *DummyWebCookware]
	Test struct {
		kitchen.SetBase[*DummyWebCookware]
		Struct kitchen.Dish[*DummyWebCookware, *DummyWebInput, DummyWebInput]
	}
}

func TestWebWrapper(t *testing.T) {
	webTest := kitchen.InitMenu[*WebTestWrapper, *DummyWebCookware](&WebTestWrapper{}, &DummyWebCookware{})

	o, err := kitchenWeb.MakeOpenApi("test", []string{"xxx.com"}, "/", "0.0.0", webTest)
	assert.Nil(t, err)
	fmt.Println(string(o))
}

func TestWeb(t *testing.T) {

	type WebTest struct {
		kitchen.MenuBase[*WebTest, *DummyWebCookware]
		Test struct {
			kitchen.SetBase[*DummyWebCookware]
			Plain     kitchen.Dish[*DummyWebCookware, string, string]
			Struct    kitchen.Dish[*DummyWebCookware, *DummyWebInput, DummyWebInput]
			UrlParams kitchen.Dish[*DummyWebCookware, *DummyUrlParamInput, DummyWebInput]
			Login     kitchen.Dish[*DummyWebCookware, any, int64]
			Manual    kitchen.Dish[*DummyWebCookware, any, any]
		}
		Player struct {
			kitchen.SetBase[*DummyWebCookware]
			Actions struct {
				kitchen.SetBase[*DummyWebCookware]
				payment kitchen.Dish[*DummyWebCookware, string, string]
			} `urlParam:"test"`
		}
	}

	webTest := kitchen.InitMenu[*WebTest, *DummyWebCookware](&WebTest{}, &DummyWebCookware{
		DummyWorkerCookwareWithTracer: DummyWorkerCookwareWithTracer{
			ITraceableCookware: kitchen.NewZeroLogTraceableCookware[DummyWorkerCookwareWithTracer](&logger),
		},
	})
	webTest.Test.Plain.SetCooker(func(ctx kitchen.IContext[*DummyWebCookware], input string) (output string, err error) {
		return input, nil
	})
	webTest.Test.Struct.SetCooker(func(i kitchen.IContext[*DummyWebCookware], input *DummyWebInput) (output DummyWebInput, err error) {
		return *input, nil
	})
	webTest.Test.UrlParams.SetCooker(func(i kitchen.IContext[*DummyWebCookware], input *DummyUrlParamInput) (output DummyWebInput, err error) {
		return DummyWebInput{A: input.Test1, B: input.Test2}, nil
	})
	webTest.Test.Login.SetCooker(func(i kitchen.IContext[*DummyWebCookware], input any) (output int64, err error) {
		return i.Cookware().UserId, nil
	})
	webTest.Test.Manual.SetCooker(func(i kitchen.IContext[*DummyWebCookware], input any) (output any, err error) {
		i.FromWeb().Response().Header().Set("Location", "https://google.com")
		return nil, nil
	})

	router := mux.NewRouter()
	helper := muxHelper.NewWrapper(router)
	helper.AddMenuToRouter(webTest)

	//auto export
	//routerMap := webTest.AddMenuToRouter(context.Background(), "/")
	//for url, handler := range routerMap {
	//	router.HandleFunc(url, handler)
	//}
	url, urlParams, method, _ := routerHelper.DishUrlAndMethod(&webTest.Test.Plain, routerHelper.DefaultUrlParamWrapper)
	assert.Equal(t, "GET", method)
	assert.Equal(t, "plain", url)
	assert.Equal(t, 1, len(urlParams))
	assert.Equal(t, "{p1}", urlParams[0][0])
	url, urlParams, method, _ = routerHelper.DishUrlAndMethod(&webTest.Test.Struct, routerHelper.DefaultUrlParamWrapper)
	assert.Equal(t, "GET", method)
	assert.Equal(t, "struct", url)
	assert.Equal(t, 3, len(urlParams))
	assert.Equal(t, "{a}", urlParams[0][0])
	assert.Equal(t, "{b}", urlParams[1][0])
	assert.Equal(t, "{c}", urlParams[2][0])

	url, urlParams, method, _ = routerHelper.DishUrlAndMethod(&webTest.Test.UrlParams, routerHelper.DefaultUrlParamWrapper)
	assert.Equal(t, "GET", method)
	assert.Equal(t, "url_params", url)
	assert.Equal(t, 2, len(urlParams))
	assert.Equal(t, "{test1}", urlParams[0][0])
	assert.Equal(t, "{test2}", urlParams[1][0])

	go func() {
		http.ListenAndServe(":18080", router)
	}()

	time.Sleep(time.Millisecond * 100)

	resp, err := http.Get("http://localhost:18080/test/plain/123")
	assert.Nil(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	data, err := io.ReadAll(resp.Body)
	assert.Nil(t, err)
	assert.Equal(t, "123", string(data))

	resp, err = http.Get("http://localhost:18080/test/struct/1/2/3")
	assert.Nil(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	data, err = io.ReadAll(resp.Body)
	assert.Nil(t, err)
	assert.Equal(t, `{"A":"1","B":"2","C":"3"}`, string(data))

	resp, err = http.Get("http://localhost:18080/test/struct/1/2/3?a=4&b=5&c=6")
	assert.Nil(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	data, err = io.ReadAll(resp.Body)
	assert.Nil(t, err)
	assert.Equal(t, `{"A":"4","B":"5","C":"6"}`, string(data))

	req, err := http.NewRequest("GET", "http://localhost:18080/test/url_params/123/234", bytes.NewBuffer([]byte(`{"Test1":"234", "Test2":"345"}`)))
	assert.Nil(t, err)
	resp, err = http.DefaultClient.Do(req)
	assert.Nil(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	data, err = io.ReadAll(resp.Body)
	assert.Nil(t, err)
	assert.Equal(t, `{"A":"123","B":"234","C":""}`, string(data))

	req, err = http.NewRequest("GET", "http://localhost:18080/test/url_params/345/456", bytes.NewBuffer([]byte(`{"Test1":"567","Test1":"678"}`)))
	assert.Nil(t, err)
	resp, err = http.DefaultClient.Do(req)
	assert.Nil(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	data, err = io.ReadAll(resp.Body)
	assert.Nil(t, err)
	assert.Equal(t, `{"A":"345","B":"456","C":""}`, string(data))

	req, err = http.NewRequest("GET", "http://localhost:18080/test/login", nil)
	assert.Nil(t, err)
	req.Header.Set("UserId", "1234") //DummyWebCookware implements IWebCookware will parse this
	resp, err = http.DefaultClient.Do(req)
	assert.Nil(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	data, err = io.ReadAll(resp.Body)
	assert.Nil(t, err)
	assert.Equal(t, `1234`, string(data))

	resp, err = http.Get("http://localhost:18080/test/manual")
	assert.Nil(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "https://google.com", resp.Header.Get("Location"))
}

func TestGrpc(t *testing.T) {
	type GrpcTest struct {
		kitchen.MenuBase[*GrpcTest, *DummyWorkerCookware]
		SayHello kitchen.Dish[*DummyWorkerCookware, *helloworld.HelloRequest, *helloworld.HelloReply]
	}
	grpcTest := kitchen.InitMenu[*GrpcTest, *DummyWorkerCookware](&GrpcTest{}, &DummyWorkerCookware{})
	grpcTest.SayHello.SetCooker(func(ctx kitchen.IContext[*DummyWorkerCookware], input *helloworld.HelloRequest) (output *helloworld.HelloReply, err error) {
		return &helloworld.HelloReply{Response: input.Request + " world"}, nil
	})

	lis, err := net.Listen("tcp", "0.0.0.0:19527")
	assert.Nil(t, err)
	encoding.RegisterCodec(vtgrpc.Codec{})
	s := grpc.NewServer()
	err = kitchenGrpc.RegisterGrpcServer(s, helloworld.Greeter_ServiceDesc, grpcTest)
	assert.Nil(t, err)
	go func() {
		if err := s.Serve(lis); err != nil {
			panic(err)
		}
	}()
	conn, err := grpc.Dial("127.0.0.1:19527", grpc.WithTransportCredentials(insecure.NewCredentials()))
	assert.Nil(t, err)
	client := helloworld.NewGreeterClient(conn)
	resp, err := client.SayHello(context.Background(), &helloworld.HelloRequest{Request: "Hello"})
	assert.Nil(t, err)
	assert.Equal(t, "Hello world", resp.Response)
}

type Filter struct {
	Ids   []int
	Users []int
}

type DummyWorkerCookware struct {
	Sql        *sql.DB
	Clickhouse *sql.DB
	//Redis      *redis.Client
	//Mq
	//Grpc
	//...
}

type DummyWorkerCookwareWithTracer struct {
	kitchen.ITraceableCookware[DummyWorkerCookwareWithTracer]
	DummyWorkerCookware
}

type DummyWebCookware struct {
	DummyWorkerCookwareWithTracer
	UserId      int64
	HttpRequest *http.Request
	HttpWriter  http.ResponseWriter
}

func (d DummyWebCookware) RequestParser(action kitchen.IDish, bundle kitchen.IWebBundle) (routerHelper.IWebCookware, error) {
	dd := d
	dd.UserId, _ = strconv.ParseInt(bundle.Headers().Get("UserId"), 10, 64)
	//dd.UserId, _ = strconv.ParseInt(r.Header.Get("UserId"), 10, 64)
	//fmt.Println(r.Header)
	//dd.HttpRequest = r
	//dd.HttpWriter = w
	return &dd, nil
}

type DummyWebInput struct {
	A string `urlParam:"a"`
	B string `urlParam:"b"`
	C string `urlParam:"c"`
}

type DummyUrlParamInput struct {
	Test1 string `urlParam:"test1"`
	Test2 string `urlParam:"test2"`
}

type DummyPipelineCookware struct {
	DummyWorkerCookwareWithTracer
	*PipelineSqlTxCookware[*PurchaseOrder]
}

func (d PurchaseOrder) GetStatus() kitchen.PipelineStatus {
	return d.Status
}

func (d *PurchaseOrder) SetStatus(status kitchen.PipelineStatus) {
	d.Status = status
}

func TestRoutineLimit(t *testing.T) {
	OrderWorkerWithTracer.ConcurrentLimit(10)
	OrderWorkerWithTracer.Pending.TestA.ConcurrentLimit(5)
	var run int64
	OrderWorkerWithTracer.Pending.TestA.SetCooker(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input int) (output int, err error) {
		time.Sleep(time.Millisecond * 100)
		atomic.AddInt64(&run, 1)
		return input + 1, nil
	})
	for i := 0; i < 10; i++ {
		go OrderWorkerWithTracer.Pending.TestA.Cook(context.Background(), i)
	}
	time.Sleep(time.Millisecond * 150)
	assert.Equal(t, int64(5), run)
}

var (
	OrderWorker           *Order
	OrderWorkerWithTracer *OrderWithTracer
)

func init() {
	OrderWorker = kitchen.InitMenu[*Order, DummyWorkerCookware](&Order{}, DummyWorkerCookware{})
	OrderWorkerWithTracer = kitchen.InitMenu[*OrderWithTracer, DummyWorkerCookwareWithTracer](&OrderWithTracer{}, DummyWorkerCookwareWithTracer{
		ITraceableCookware: kitchen.NewZeroLogTraceableCookware[DummyWorkerCookwareWithTracer](&logger),
	})
	OrderWorker.Pending.TestA.SetCooker(func(ctx kitchen.IContext[DummyWorkerCookware], input int) (output int, err error) {
		return input + 1, nil
	})
	OrderWorker.Util.TestC.SetCooker(func(ctx kitchen.IContext[DummyWorkerCookware], input int) (output int, err error) {
		var (
			dummy = make([]int, 100000)
		)
		return len(dummy), nil
	})
	OrderWorkerWithTracer.Pending.TestA.SetCooker(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input int) (output int, err error) {
		return input + 1, nil
	}).AfterCook(func(ctx kitchen.IContext[DummyWorkerCookwareWithTracer], input, output int, err error) {
		input++
	})

}
func BenchmarkExec(b *testing.B) {
	var res int
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		if res, _ = OrderWorker.Pending.TestA.Cook(ctx, i); res != i+1 {
			b.Fail()
		}
	}
}
func BenchmarkExecWithTracer(b *testing.B) {
	var res int
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		if res, _ = OrderWorkerWithTracer.Pending.TestA.Exec(ctx, i); res != i+1 {
			b.Fail()
		}
	}
}
func BenchmarkExecWithTracerOff(b *testing.B) {
	var (
		res    int
		logger = logger.Level(zerolog.Disabled)
	)
	OrderWorkerWithTracer.Dependency().ITraceableCookware.(*kitchen.ZeroLogTraceableCookware[DummyWorkerCookwareWithTracer]).Logger = &logger
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		if res, _ = OrderWorkerWithTracer.Pending.TestA.Exec(ctx, i); res != i+1 {
			b.Fail()
		}
	}
}

type PipelineSqlTxCookware[M kitchen.IPipelineModel] struct {
	db *sql.DB
}

func NewPipelineSqlTxCookware[M kitchen.IPipelineModel](db *sql.DB) *PipelineSqlTxCookware[M] {
	return &PipelineSqlTxCookware[M]{db: db}
}

func (p PipelineSqlTxCookware[M]) BeginTx(ctx context.Context, opts ...*sql.TxOptions) (kitchen.IDbTx, error) {
	return &sql.Tx{}, nil
}

func (p PipelineSqlTxCookware[M]) FinishTx(tx kitchen.IDbTx, err error) error {
	return nil
}

type PurchaseOrder struct {
	Id     int
	Status kitchen.PipelineStatus
}

func (p PurchaseOrder) PrimaryKey() any {
	return p.Id
}

func (p PipelineSqlTxCookware[M]) GetModelById(ctx context.Context, id ...any) (model M, err error) {
	return any(&PurchaseOrder{Status: "Pending"}).(M), nil
}

func (p PipelineSqlTxCookware[M]) SaveModel(db kitchen.IDbRunner, model M, oStatus kitchen.PipelineStatus) error {
	fmt.Println("save model", model)
	return nil
}
