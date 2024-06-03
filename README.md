# Kitchen Framework

A golang framework for building progressive backend services.

![](./docs/asset/cover.jpeg)

## Introduction

Kitchen is a framework designed for building progressive, scalable services.

The core concept of Kitchen is to create placeholders for all major functions. These placeholders define the input, output, and dependencies required for execution. Function bodies can then be assigned to these placeholders.

At runtime, functions are invoked from the placeholders. This allows for the integration of additional logic such as logging, tracing, metrics, callbacks, and more into the function calls without messing up the code.

Since execution is managed via placeholders, the call is not necessarily executed in local. This enables seamless upgrading of the service from a monolithic architecture to horizontal monoliths, and even splitting its scope to microservices.
# Overview

## Key Features

- Tracing / logging / metrics
- Effortless/Restartless scaling
- Dependencies management
- Concurrency management
- Plugin for serving as web / gRPC API
- OpenApi schema generation
- Pipeline for state management
- Asynchronous call

## Introduction

There are few components in the framework that are used to build the API. Let's take an abstract example of a cafe.

```go

// In a cafe, what we focus on is to serve the customer with DISH, like coffee, cake etc.
// DISH is the placeholder for the function

// COOKWARE is the dependency that is required for serving the request.
type coffeeCookware struct {
    grinder *Grinder
    coffeeMachine *CoffeeMachine
}

// MENU is the collection of DISH
// Each MENU will be assigned a COOKWARE
// All the DISHes in the MENU will be using the same COOKWARE to serve
type CoffeeMenu struct {
    kitchen.MenuBase[*CoffeeMenu, coffeeCookware] // Base struct for the menu
    Cappuccino kitchen.Dish[                // Dish placeholder
	    coffeeCookware,                     // Cookware
		*cafeProto.CappuccinoOrder,         // Input, like specifications of the coffee like milk, beans etc.
		*cafeProto.CappuccinoOutput,        // Output, your coffee!
	]
}

// Another cookware for cake
type cakeCookware struct {
    //oven
    //mixer
}

// Cakes need another menu since they use different cookware
type CakeMenu struct {
    kitchen.MenuBase[*CakeMenu, cakeCookware]
    Tiramisu kitchen.Dish[cakeCookware, *cafeProto.TiramisuOrder, *cafeProto.TiramisuOutput]
}

func main() {
    coffeeMenu := kitchen.InitMenu(       // Initialize the menu
		new(CoffeeMenu),                  // Menu prototype
		newCoffeeCookwarePool(),          // Cookware, can be a reusable pointer/sync.Pool
		)
	
	// Assign the Cooker to the Dish, which is the actual function body
    coffeeMenu.Cappuccino.SetCooker(func(
		ctx kitchen.IContext[coffeeCookware],    // Context contain the cookware and lifecycle utils
		input *testProto.CappuccinoOrder) 
	    (*testProto.CappuccinoOutput, error) {
    
        //let's cook!
        grindedBeans := ctx.Cookware().grinder.grindBeans(input.Beans)
        coffeeReady := ctx.Cookware().coffeeMachine.brewCoffee(grindedBeans, input.Milk)
        
        return &testProto.CappuccinoOutput{ Cappuccino: coffeeReady}, nil // return error or the coffee, enjoy!
    })

	// Try a callback after the cooking is done
    coffeeMenu.Cappuccino.AfterCook(func(ctx kitchen.IContext[coffeeCookware], input *testProto.CappuccinoOrder, output *testProto.CappuccinoOutput, err error) {
        ctx.Cookware().coffeeMachine.clean() // clean the machine after brewing
    })

	//cook the coffee
    coffee, res := coffeeMenu.Cappuccino.Cook(context.Background(), &testProto.CappuccinoOrder{Beans: "arabica", Milk: "whole"}
}


```

## Web API Plugin

```go
type WebTest struct {
    kitchen.MenuBase[*WebTest, *WebCookware]
    HelloWorld kitchen.Dish[*WebCookware, string, string]       // url: /hello_world
    Login     kitchen.Dish[*WebCookware, any, int64]            // url: /login
}

//special cookware for web
type WebCookware struct {
    UserId      int64
}

// Implement the IWebCookware to parse the request into cookware with session
func (d WebCookware) RequestParser(action kitchen.IDish, bundle kitchen.IWebBundle) (routerHelper.IWebCookware, error) {
    dd := d
    dd.UserId, _ = strconv.ParseInt(bundle.Headers().Get("UserId"), 10, 64)
    return &dd, nil
}

func main() {
    webTest := kitchen.InitMenu(new(WebTest), newWebCookwarePool())
    webTest.HelloWorld.SetCooker(func(ctx kitchen.IContext[*WebCookware], input string) (string, error) {
        return input+" world!", nil
    })
    webTest.Login.SetCooker(func(ctx kitchen.IContext[*WebCookware], input any) (int64, error) {
        return 1, nil
    })
    
    router := mux.NewRouter()                  // Prepare the router
    helper := muxHelper.NewWrapper(router)     // mux router helper, also available for echo/fasthttp
    helper.AddMenuToRouter(webTest)
    //helper.AddMenuToRouter(anotherMenu)
    
	// Generate the openapi schema
    jsonBytes, err := kitchenWeb.MakeOpenApi(someApiName, []string{"host1.com"}, "/", someVersion, webTest)
    
    http.ListenAndServe(":8080", router)
}
	
	
```

## gRPC API Plugin

```go

//setup the gRPC menu following the proto service signature
type GrpcMenu struct {
    kitchen.MenuBase[*GrpcTest, *AnyGrpcCookware]
    SayHello kitchen.Dish[*DummyWorkerCookware, *helloworld.HelloRequest, *helloworld.HelloReply]
}

func main() {
	
	// initialize the menu as usual
	grpcTest := kitchen.InitMenu[*GrpcMenu, *AnyGrpcCookware](&GrpcTest{}, &AnyGrpcCookware{})
	grpcTest.SayHello.SetCooker(func(ctx kitchen.IContext[*AnyGrpcCookware], input *helloworld.HelloRequest) (output *helloworld.HelloReply, err error) {
		return &helloworld.HelloReply{Response: input.Request + " world"}, nil
	})
	
	// start the gRPC server
	lis, err := net.Listen("tcp", "0.0.0.0:19527")
	if err != nil {
        panic(err)
    }
	s := grpc.NewServer()
	// Register the gRPC server with the plugin
	err = kitchenGrpc.RegisterGrpcServer(s, helloworld.Greeter_ServiceDesc, grpcTest)
	if err != nil {
        panic(err)
    }
    if err := s.Serve(lis); err != nil {
        panic(err)
    }
}
```




# Contributors
- [Dillion Kum](https://github.com/dkishere)
- [vali637](https://github.com/vali637)
- [SuicaLondon](https://github.com/SuicaLondon)
- [Eleron8](https://github.com/Eleron8)
- [aditya1604](https://github.com/aditya1604)
- [mrtztg](https://github.com/mrtztg)