package main

import (
	"context"
	"fmt"
	"github.com/fasthttp/router"
	"github.com/go-preform/kitchen"
	testProto "github.com/go-preform/kitchen/test/proto"
	"github.com/go-preform/kitchen/web/routerHelper/fasthttpHelper"
	"github.com/valyala/fasthttp"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type coffeeCookware struct {
	//grinder
	//coffee machine
}

type cakeCookware struct {
	//oven
	//mixer
}

type setCookware struct {
	cakeCookware
	coffeeCookware
}

type menus struct {
	coffeeMenu *CoffeeMenu
	cakeMenu   *CakeMenu
	setMenu    *SetMenu
}

type CoffeeMenu struct {
	kitchen.MenuBase[*CoffeeMenu, coffeeCookware]
	Cappuccino kitchen.Dish[coffeeCookware, *testProto.CappuccinoInput, *testProto.CappuccinoOutput]
}

type CakeMenu struct {
	kitchen.MenuBase[*CakeMenu, cakeCookware]
	Tiramisu kitchen.Dish[cakeCookware, *testProto.TiramisuInput, *testProto.TiramisuOutput]
}

type SetMenu struct {
	kitchen.MenuBase[*SetMenu, setCookware]
	CakeAndCoffee kitchen.Dish[setCookware, *testProto.SetInput, *testProto.SetOutput]
}

func newMenus() (*menus, []int) {
	cnt := []int{0, 0, 0}
	coffeeMenu := kitchen.InitMenu(new(CoffeeMenu), coffeeCookware{})
	coffeeMenu.Cappuccino.SetCooker(func(ctx kitchen.IContext[coffeeCookware], input *testProto.CappuccinoInput) (*testProto.CappuccinoOutput, error) {
		for i := 0; i < 1000000; i++ {
			_ = i ^ 2 ^ 2 ^ 2 ^ 2
		} //simulate cooking time
		cnt[0]++
		return &testProto.CappuccinoOutput{Cappuccino: "Cappuccino with " + input.Beans + " beans and " + input.Milk + " milk"}, nil
	})
	cakeMenu := kitchen.InitMenu(new(CakeMenu), cakeCookware{})
	cakeMenu.Tiramisu.SetCooker(func(ctx kitchen.IContext[cakeCookware], input *testProto.TiramisuInput) (*testProto.TiramisuOutput, error) {
		for i := 0; i < 1000000; i++ {
			_ = i ^ 2 ^ 2 ^ 2 ^ 2
		} //simulate cooking time
		cnt[1]++
		return &testProto.TiramisuOutput{Tiramisu: "Tiramisu with " + input.Cheese + " cheese, " + input.Coffee + " coffee and " + input.Wine + " wine"}, nil
	})
	setMenu := kitchen.InitMenu(new(SetMenu), setCookware{})
	setMenu.CakeAndCoffee.SetCooker(func(ctx kitchen.IContext[setCookware], input *testProto.SetInput) (*testProto.SetOutput, error) {
		var (
			resp = &testProto.SetOutput{}
			err  error
		)
		cnt[2]++
		resp.Tiramisu, err = cakeMenu.Tiramisu.Cook(ctx, input.Tiramisu)
		if err != nil {
			return nil, err
		}
		resp.Cappuccino, err = coffeeMenu.Cappuccino.Cook(ctx, input.Cappuccino)
		return resp, err
	})
	return &menus{coffeeMenu, cakeMenu, setMenu}, cnt
}

var (
	mgrMenu1 *menus
	mgr      kitchen.IManager
)

var orderCnt1 = []int{0, 0, 0}

func init() {
	ctx, cancel := context.WithCancel(context.Background())

	menuStrs := os.Getenv("MENUS")

	localAddr := os.Getenv("LOCAL_ADDR")
	localPort, _ := strconv.ParseUint(os.Getenv("LOCAL_PORT"), 10, 64)
	hostAddr := os.Getenv("HOST_ADDR")
	hostPort, _ := strconv.ParseUint(os.Getenv("HOST_PORT"), 10, 64)

	httpAddr := os.Getenv("HTTP_ADDR")
	httpPort, _ := strconv.ParseUint(os.Getenv("HTTP_PORT"), 10, 64)
	if httpAddr == "" {
		httpAddr = "127.0.0.1"
	}
	if httpPort == 0 {
		httpPort = 80
	}
	if localAddr == "" {
		localAddr = "tcp://127.0.0.1"
	}
	if localPort == 0 {
		localPort = 10001
	}

	fmt.Println("init", httpAddr, httpPort, localAddr, localPort, hostAddr, hostPort)

	var (
		err error
	)

	mgr = kitchen.NewDeliveryManager(localAddr, uint16(localPort))
	if hostAddr != "" && hostPort != 0 {
		mgr.SetMainKitchen(hostAddr, uint16(hostPort))
	}
	mgrMenu1, orderCnt1 = newMenus()
	mgr, err = mgr.AddMenu(func() kitchen.IMenu {
		return mgrMenu1.coffeeMenu
	}).AddMenu(func() kitchen.IMenu {
		return mgrMenu1.cakeMenu
	}).AddMenu(func() kitchen.IMenu {
		return mgrMenu1.setMenu
	}).Init(ctx)
	if err != nil {
		panic(err)
	}
	if menuStrs != "" {
		if menuStrs == "none" {
			mgr.DisableMenu("CoffeeMenu")
			mgr.DisableMenu("CakeMenu")
			mgr.DisableMenu("SetMenu")
		} else {
			menus := strings.Split(menuStrs, ",")
			mgr.SelectServeMenus(menus...)
		}
	}
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		cancel()
		time.Sleep(time.Second)
	}()
}

func main() {
	httpAddr := os.Getenv("HTTP_ADDR")
	httpPort, _ := strconv.ParseUint(os.Getenv("HTTP_PORT"), 10, 64)
	if httpAddr == "" {
		httpAddr = "0.0.0.0"
	}
	if httpPort == 0 {
		httpPort = 80
	}

	fastHttpRouter := router.New()
	fasthttpHelper.NewWrapper(fastHttpRouter).
		AddMenuToRouter(mgrMenu1.coffeeMenu).
		AddMenuToRouter(mgrMenu1.cakeMenu).
		AddMenuToRouter(mgrMenu1.setMenu)

	fastHttpRouter.GET("/count", func(ctx *fasthttp.RequestCtx) {
		_, _ = ctx.WriteString(fmt.Sprintf("Cappuccino: %d, Tiramisu: %d, Set: %d", orderCnt1[0], orderCnt1[1], orderCnt1[2]))
	})
	fastHttpRouter.GET("/cappuccino_local", func(ctx *fasthttp.RequestCtx) {
		for i := 0; i < 1000000; i++ {
			_ = i ^ 2 ^ 2 ^ 2 ^ 2
		}
		input := &testProto.CappuccinoInput{Beans: string(ctx.QueryArgs().Peek("Beans")), Milk: string(ctx.QueryArgs().Peek("Milk"))}
		orderCnt1[0]++
		_, _ = ctx.WriteString("Cappuccino with " + input.Beans + " beans and " + input.Milk + " milk")
	})
	//dynamic disable menu
	fastHttpRouter.GET("/disable", func(ctx *fasthttp.RequestCtx) {
		mgr.DisableMenu(string(ctx.QueryArgs().Peek("menu")))
		_, _ = ctx.WriteString("Disabled:" + string(ctx.QueryArgs().Peek("menu")))
	})

	go func() {
		//pprof
		log.Println(http.ListenAndServe(fmt.Sprintf("%s:8080", httpAddr), nil))
	}()

	if err := fasthttp.ListenAndServe(fmt.Sprintf("%s:%d", httpAddr, httpPort), fastHttpRouter.Handler); err != nil {
		log.Fatalf("Error in ListenAndServe: %v", err)
	}

}
