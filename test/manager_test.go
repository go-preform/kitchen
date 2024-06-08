package main

import (
	"context"
	"fmt"
	"github.com/go-preform/kitchen"
	testProto "github.com/go-preform/kitchen/test/proto"
	"github.com/stretchr/testify/assert"
	"testing"
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
		cnt[0]++
		return &testProto.CappuccinoOutput{Cappuccino: "Cappuccino with " + input.Beans + " beans and " + input.Milk + " milk"}, nil
	})
	cakeMenu := kitchen.InitMenu(new(CakeMenu), cakeCookware{})
	cakeMenu.Tiramisu.SetCooker(func(ctx kitchen.IContext[cakeCookware], input *testProto.TiramisuInput) (*testProto.TiramisuOutput, error) {
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
	mgr1 kitchen.IManager //normally, you should use a single manager in a service
	mgr2 kitchen.IManager
	mgr3 kitchen.IManager

	mgrMenu1 *menus
	mgrMenu2 *menus
	mgrMenu3 *menus

	orderCnt1 []int
	orderCnt2 []int
	orderCnt3 []int
)

func init() {
	fmt.Println("init")
	var (
		err error
	)

	mgr1 = kitchen.NewDeliveryManager("tcp://127.0.0.1", 20000)
	mgrMenu1, orderCnt1 = newMenus()
	mgr1, err = mgr1.AddMenu(func() kitchen.IMenu {
		return mgrMenu1.coffeeMenu
	}).AddMenu(func() kitchen.IMenu {
		return mgrMenu1.cakeMenu
	}).AddMenu(func() kitchen.IMenu {
		return mgrMenu1.setMenu
	}).Init(context.Background())
	if err != nil {
		panic(err)
	}
	//fmt.Println("mgr1 ready")

	mgr2 = kitchen.NewDeliveryManager("tcp://127.0.0.1", 20001)
	mgrMenu2, orderCnt2 = newMenus()
	mgr2, err = mgr2.AddMenu(func() kitchen.IMenu {
		return mgrMenu2.coffeeMenu
	}).AddMenu(func() kitchen.IMenu {
		return mgrMenu2.cakeMenu
	}).AddMenu(func() kitchen.IMenu {
		return mgrMenu2.setMenu
	}).SetMainKitchen("tcp://127.0.0.1", 20000).Init(context.Background())
	if err != nil {
		panic(err)
	}
	//fmt.Println("mgr2 ready")

	mgr3 = kitchen.NewDeliveryManager("tcp://127.0.0.1", 20002)
	mgrMenu3, orderCnt3 = newMenus()
	mgr3, err = mgr3.AddMenu(func() kitchen.IMenu {
		return mgrMenu3.coffeeMenu
	}).
		AddMenu(func() kitchen.IMenu {
			return mgrMenu3.cakeMenu
		}).
		AddMenu(func() kitchen.IMenu {
			return mgrMenu3.setMenu
		}).
		SetMainKitchen("tcp://127.0.0.1", 20000).Init(context.Background())
	if err != nil {
		panic(err)
	}
	//fmt.Println("mgr3 ready")
	fmt.Println("ready")
	time.Sleep(1000 * time.Millisecond)
}

func TestForeign(t *testing.T) {
	mgr1.SelectServeMenus("CoffeeMenu")
	mgr2.SelectServeMenus("CakeMenu")
	mgr3.SelectServeMenus("SetMenu")
	time.Sleep(500 * time.Millisecond)
	var (
		ctx = context.Background()
	)
	coffee, err := mgrMenu1.coffeeMenu.Cappuccino.Cook(ctx, &testProto.CappuccinoInput{Beans: "Arabica", Milk: "Whole"})
	assert.Nil(t, err)
	assert.Equal(t, "Cappuccino with Arabica beans and Whole milk", coffee.Cappuccino)
	assert.Equal(t, 1, orderCnt1[0])

	cake, err := mgrMenu1.cakeMenu.Tiramisu.Cook(ctx, &testProto.TiramisuInput{Cheese: "Mascarpone", Coffee: "Espresso", Wine: "Marsala"})
	assert.Nil(t, err)
	assert.Equal(t, "Tiramisu with Mascarpone cheese, Espresso coffee and Marsala wine", cake.Tiramisu)
	assert.Equal(t, 1, orderCnt2[1])

	set, err := mgrMenu1.setMenu.CakeAndCoffee.Cook(ctx, &testProto.SetInput{
		Tiramisu:   &testProto.TiramisuInput{Cheese: "Mascarpone", Coffee: "Espresso", Wine: "Marsala"},
		Cappuccino: &testProto.CappuccinoInput{Beans: "Arabica", Milk: "Whole"},
	})
	assert.Nil(t, err)
	assert.Equal(t, "Tiramisu with Mascarpone cheese, Espresso coffee and Marsala wine", set.Tiramisu.Tiramisu)
	assert.Equal(t, "Cappuccino with Arabica beans and Whole milk", set.Cappuccino.Cappuccino)
	assert.Equal(t, 0, orderCnt3[0])
	assert.Equal(t, 0, orderCnt3[1])
	assert.Equal(t, 1, orderCnt3[2])
	assert.Equal(t, 2, orderCnt2[1])
	assert.Equal(t, 2, orderCnt1[0])

}

func TestLoadBalance(t *testing.T) {
	mgr2.SelectServeMenus("CoffeeMenu")
	mgr3.SelectServeMenus("CoffeeMenu")
	time.Sleep(500 * time.Millisecond)
	var (
		ctx = context.Background()
	)

	for i := 0; i < 20; i++ {
		_, err := mgrMenu1.coffeeMenu.Cappuccino.Cook(ctx, &testProto.CappuccinoInput{Beans: "Arabica", Milk: "Whole"})
		assert.Nil(t, err)
	}
	assert.NotEqual(t, 2, orderCnt1[0])
	assert.NotEqual(t, 0, orderCnt2[0])
	assert.NotEqual(t, 0, orderCnt3[0])

	fmt.Println(orderCnt1)
	fmt.Println(orderCnt2)
	fmt.Println(orderCnt3)

}

func BenchmarkOrderCoffeeLoadBalance(b *testing.B) {
	var (
		ctx = context.Background()
		in  = &testProto.CappuccinoInput{Beans: "Arabica", Milk: "Whole"}
	)
	b.SetParallelism(100)
	b.RunParallel(func(pb *testing.PB) {
		var (
			err    error
			coffee *testProto.CappuccinoOutput
		)
		for pb.Next() {
			coffee, err = mgrMenu1.coffeeMenu.Cappuccino.Cook(ctx, in)
			assert.Nil(b, err)
			assert.Equal(b, "Cappuccino with Arabica beans and Whole milk", coffee.Cappuccino)
		}
	})
	//fmt.Println(1, orderCnt1)
	//fmt.Println(2, orderCnt2)
	//fmt.Println(3, orderCnt3)
}

//func BenchmarkHttp(b *testing.B) {
//	b.SetParallelism(1000)
//	b.RunParallel(func(pb *testing.PB) {
//		var (
//			err  error
//			resp *http.Response
//			data []byte
//		)
//		for pb.Next() {
//			resp, err = http.Get("http://127.0.0.1/cappuccino")
//			assert.Nil(b, err)
//			data, _ = io.ReadAll(resp.Body)
//			assert.Equal(b, "Cappuccino with Arabica beans and Whole milk", string(data))
//		}
//	})
//	//fmt.Println(1, orderCnt1)
//	//fmt.Println(2, orderCnt2)
//	//fmt.Println(3, orderCnt3)
//}

//func TestDisable(t *testing.T) {
//	mgr1.DisableMenu("CoffeeMenu")
//	mgr2.DisableMenu("CoffeeMenu")
//	mgr3.DisableMenu("CoffeeMenu")
//	time.Sleep(500 * time.Millisecond)
//	var (
//		ctx = context.Background()
//	)
//
//	_, err := mgrMenu1.coffeeMenu.Cappuccino.Cook(ctx, &testProto.CappuccinoInput{Beans: "Arabica", Milk: "Whole"})
//	assert.Equal(t, delivery.ErrMenuNotServing, err)
//
//}
