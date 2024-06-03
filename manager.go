package kitchen

import (
	"context"
	"errors"
	"github.com/go-preform/kitchen/delivery"
	"sync"
)

var (
	errRunInLocal = errors.New("run in local")
)

type Manager struct {
	menus            map[string]IMenu
	menuById         []IMenu
	menuInitializers []func() IMenu
	serveMenuNames   []string
	kitchens         []IKitchen
	lock             sync.Mutex
	server           delivery.ILogistic
	localHostUrl     string
	localRepPort     uint16
	hostUrl          string
	repPort          uint16
}

func NewDeliveryManager(localHostUrl string, localRepPort uint16) IManager {
	return &Manager{
		localHostUrl: localHostUrl,
		localRepPort: localRepPort,
	}
}

// not select = all
func (m *Manager) SelectServeMenus(menuNamesNilIsAll ...string) IManager {
	m.lock.Lock()
	m.serveMenuNames = menuNamesNilIsAll
	m.lock.Unlock()
	if m.server != nil {
		m.server.SetOrderHandlerPerMenu(m.getOrderHandlers())
	}
	return m
}

func (m *Manager) DisableMenu(name string) IManager {
	m.lock.Lock()
	if m.serveMenuNames == nil {
		m.serveMenuNames = make([]string, len(m.menuById))
		for i, menu := range m.menuById {
			m.serveMenuNames[i] = menu.Name()
		}
	}
	for i, n := range m.serveMenuNames {
		if n == name {
			m.serveMenuNames = append(m.serveMenuNames[:i], m.serveMenuNames[i+1:]...)
		}
	}
	m.lock.Unlock()
	if m.server != nil {
		m.server.SetOrderHandlerPerMenu(m.getOrderHandlers())
	}
	return m
}

func (m *Manager) Init() (IManager, error) {
	m.initMenus()
	var (
		err error
	)
	m.server = delivery.NewServer(m.localHostUrl, m.localRepPort, m.hostUrl, m.repPort)
	var (
		handlers = make([]func(context.Context, *delivery.Order), len(m.menuById))
	)
	if len(m.serveMenuNames) > 0 {
		for _, menuName := range m.serveMenuNames {
			handlers[m.menus[menuName].ID()] = m.menuById[m.menus[menuName].ID()].orderDish
		}
	} else {
		for _, menu := range m.menuById {
			handlers[menu.ID()] = menu.orderDish
		}
	}
	m.server.SetOrderHandlerPerMenu(handlers)
	err = m.server.Init()
	if err != nil {
		return nil, err
	}
	return m, err
}

func (m *Manager) getOrderHandlers() []func(context.Context, *delivery.Order) {
	var (
		handlers = make([]func(context.Context, *delivery.Order), len(m.menuById))
	)
	m.lock.Lock()
	if m.serveMenuNames == nil {
		for _, menu := range m.menuById {
			handlers[menu.ID()] = menu.orderDish
		}
	} else {
		for _, menuName := range m.serveMenuNames {
			handlers[m.menus[menuName].ID()] = m.menuById[m.menus[menuName].ID()].orderDish
		}
	}
	m.lock.Unlock()
	return handlers
}

func (m *Manager) initMenus() {
	m.menus = make(map[string]IMenu)
	m.menuById = make([]IMenu, len(m.menuInitializers))
	for i, menuInitializer := range m.menuInitializers {
		menu := menuInitializer()
		menu.setManager(m, uint32(i))
		if len(m.serveMenuNames) > 0 {
			for _, name := range m.serveMenuNames {
				if menu.Name() == name {
					m.menus[name] = menu
					m.menuById[i] = menu
					break
				}
			}
		} else {
			m.menus[menu.Name()] = menu
			m.menuById[i] = menu
		}
	}
}

func (m *Manager) AddMenu(menuInitializer func() IMenu) IManager {
	m.menuInitializers = append(m.menuInitializers, menuInitializer)
	return m
}

func (m *Manager) SetMainKitchen(url string, port uint16) IManager {
	m.hostUrl = url
	m.repPort = port
	if m.server != nil {
		m.server.SwitchLeader(url, port)
	}
	return m
}

func (m *Manager) Order(dish IDish) (func(ctx context.Context, input []byte) (output []byte, err error), error) {
	return m.server.Order(uint16(dish.Menu().ID()), uint16(dish.Id()))
}
