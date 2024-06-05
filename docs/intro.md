# Microservices vs Monolith, why not both with one codebase?

As a backend developer and system architect, the biggest decision I have to make is how to balance the trade-offs
of being simple, efficient, and scalable: keep the system in a monolithic architecture or split it into microservices.

### Microservices

It’s usually considered as the advanced choice by most developers. Indeed, yes, it’s the ultimate solution. And the team
can enjoy some benefits other than performance and scalability, like independent deployment, minimized downtime,
flexible technology stack, etc. I can understand why developers are so keen on it. However, the trade-offs are
also obvious: complexity and cost. It’s just too hard! Every time when developers suggest to use a microservices
architecture, I will question whether we can do it right. To be honest, I am not that confident too, especially
after certain updates. Please take a look at this if you doubt that.

[<img src="./asset/intro_ms1.png" alt="microservice by KRAZAM" style="width:100%;"/>](https://www.youtube.com/watch?v=y8OnoxKotPQ)

I can't stop laughing every time I watch it, but it's bloody true. You may read the comments and the [ThePrimeTime's reaction](https://www.youtube.com/watch?v=s-vJcOfrvi0) to it.
That’s not hard to understand, right? Just consider the spaghetti code you’ve ever written and imagine
they are now in network calls—an absolute nightmare.

### Monolith

I do love monoliths. If we can’t foresee a million users or thousands of concurrency, who needs microservices?
It’s way easier to develop, deploy, and maintain. No need to worry about service discovery, inter-service
portals, network drops, latency, blocking, timeouts, etc. And as hardware performance and CI/CD techniques
improve, a monolith on a 100+ core server with proper pipelines can well support thousands of transactions 
per second with minimal downtime. And FYR, the request per second of Google search is 40K rps in avg.
And if a 100 cores server is too expensive in long run, well-designed monolith can auto-scale itself horizontally;
I would say >90% of the time it’s sufficient.

### Modular Monolith

After considering all the pros and cons, developers nowadays seem not that into microservices. Big tech
companies like Amazon and Google are moving some of their less intensive products back to monoliths.
But of course, these are big companies, and they always prepare for growing fast and big in the foreseeable future.
So modular monoliths approach is their choice. In short, it’s some microservices running in the same process
but still calling each other through network APIs. It’s obviously easier than microservices and yet more
scalable than an ordinary monolith. But from my understanding, it’s not that ideal, just like the graph below.

<img src="./asset/intro_cmp1.png" alt="introduction caparison" style="width:100%;"/>

The overhead of coding is still there no matter if you use RESTful, gRPC, or MQ. It requires a lot more
effort to manage the communication. And obviously, the performance overhead is there since it uses network
calls regardless of the fact that they are in the same process. Even if they can overcome that with some
helper like a fake client/server, it still increases the cost of implementation/splitting. Moreover, in
case it needs to split one day, the cost is still not that low, especially when taking deployment into
consideration.

## My attempt: Kitchen

After all of these, I’ve come up with my own solution: Kitchen. I’ve set some goals for it:

1. Minimal development overhead
1. Minimal performance overhead
1. Seamless scaling
1. Manageable call stack
1. Manageable dependencies

### How it works

The core concept of Kitchen is to create placeholders for all major functions. These placeholders define the input, output, and dependencies required for execution. Function bodies can then be assigned to these placeholders.

At runtime, functions are invoked from the placeholders. This allows for the integration of additional logic such as logging, tracing, metrics, callbacks, and more into the function calls without messing up the code.

And most importantly, since execution is called via placeholders, it is not necessarily executed in local. Let's see how it hit my goals!

### Minimal development overhead

The major overhead of using Kitchen is to predefine the placeholders before coding the actual logic.

```go
type SomeTaskes struct {
    kitchen.MenuBase[*SomeTaskes, *SomeDependecy]
    Task1 kitchen.Dish[*SomeDependecy, *SomeInput1, *SomeOutput1]
    Task2 kitchen.Dish[*SomeDependecy, string, int]
}

someTaskes := kitchen.InitMenu(&SomeTaskes{}, &SomeDependecy{})
someTaskes.Task1.SetCooker(func(dep *SomeDependecy, input *SomeInput1) (output *SomeOutput1, err error) {
    // do something with input and dependency and return output or error
})
someTaskes.Task2.SetCooker(doSomethingFn)

output1, err := someTaskes.Task1.Cook(input1)
```

The terminology is inspired by a real commercial kitchen, where:

- Dishes is some delicious food available to order, representing the function placeholder
- Cooker is the chef who assign to cooks the dish, representing the function body
- Cookware is the tools required to cook the dish, representing the dependency
- Menu is the collection of dishes, representing the module

It takes some time, of course, but compared to drafting GRPC or OpenApi, it’s way easier and faster.
And Kitchen provides convenient plugins for turning the placeholders into web APIs, generating OpenAPI
schema, and gRPC adapter, etc.

### Minimal performance overhead

To have a framework for real battles, I try hard to put performance into first place, minimize the
use of reflect or map. It makes the local call overhead is as low as <400ns, and <10000ns for foreign calls.

The network helper is based on [ZeroMQ](https://github.com/go-zeromq/zmq4), which is a high-throughput,
low-latency networking library.
It’s fast, and I’ve further improved it by implementing a 2-way data socket.
Every separated node will have a pool of TCP connections for sending requests, but unlike traditional
network calls, they just send without waiting for response/blocking. 
After the request is processed from receiver node, the response will be sent back through the request
connections targeted to the requester. This two-way data flow enabled a true async call, which is optimal
for microservices use cases.

```shell
goos: linux
goarch: amd64
pkg: github.com/go-preform/kitchen/examples/benchmark
cpu: 13th Gen Intel(R) Core(TM) i9-13900K
BenchmarkGrpc
BenchmarkGrpc-32           	  353485	      3143 ns/op
BenchmarkZMQGo
BenchmarkZMQGo-32          	  180754	      6770 ns/op
BenchmarkZMQGo2Way
BenchmarkZMQGo2Way-32      	  968262	      1166 ns/op
PASS
```

Note that the network helper is interchangeable, you may implement other helper to suit the requirement,
for example a MQ adapter is ideal for services require the highest level of durability.

### Seamless scaling

The network helper is designed to be configurable at runtime; services can easily replicate themselves
into horizontal monoliths. Or they can even toggle feature scopes at runtime to become single responsibility
services. The changes can be made by CI/CD pipeline or even APIs without restarting. The core logic can stay
unchanged as long as the dependencies are managed properly and the placeholders are properly defined.

<img src="./asset/intro_chart1.png" alt="introduction chart" style="width:100%;"/>
(run on windows docker desktop, i9-13900K allocate 4cores per container)

### Manageable call stack

Consider the placeholder as an internal URI; you can add all the things you previously
did with middlewares to it.

Metrics, logging, tracing, etc. are easy to implement; you can easily add them to the placeholders. The logger,
tracer plugins are provided by default, and additional plugins can be added by either through the tracer interface or
as an afterCook callback. 

It also comes with a concurrent limit control mechanism configurable in each placeholder or in parent level,
which helps to prevent part of a service from draining all the resources.
Unfortunately, the memory profile library provided by Golang messes up the generic type, so
I can’t provide memory limit control.

### Manageable dependencies

It’s important to keep dependencies manageable as we aim to split the modules one day. It supports both singleton
or factory/sync pool patterns. And they are injected into the function body inside the context parameter.

I am planning to add a dependency initialization and dispose interface to let Kitchen prepare it for the
corresponding functions that are enabled and release resources when they are disabled.

And it can perform some extra logic like server middlewares by implementing certain interfaces, for example,
IWebCookware for parsing web requests into cookware enables handling sessions, ACL, etc.

### Conclusion

As a system architect, I am always dreaming of a perfect solution that can balance all the trade-offs,
something that’s easy to develop, deploy, and maintain, yet scalable and efficient. One set of code suits
all the scenarios with minimal modification, progressively scaling as the business grows.

Kitchen is my attempt to achieve this. The current implementation is still in the early stage, but the 
underlying concept is solid. There are much we can do with the placeholders, and I am excited to explore
the possibilities.

I will keep improving it and hope it can help you as well. Any feedback is welcome.

<img src="./asset/cover.jpeg" alt="cover" style="width:100%;"/>

Thank you for reading.
