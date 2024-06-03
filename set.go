package kitchen

import (
	"reflect"
	"sync"
)

var (
	typeOfMenu           = reflect.TypeOf((*IMenu)(nil)).Elem()
	typeOfDish           = reflect.TypeOf((*IDish)(nil)).Elem()
	typeOfPipelineAction = reflect.TypeOf((*IPipelineAction)(nil)).Elem()
)

type SetBase[D ICookware] struct {
	cookbook[D, any, any]
	_menu     iMenu[D]
	name      string
	parentSet []iSet[D]
	self      iSet[D]
	path      *string
}

func initSet[D ICookware](menu iMenu[D], group iSet[D], parent iSet[D], name string) {
	group.init(menu, group, parent, name)
}

func (s *SetBase[D]) init(p iMenu[D], group, parent iSet[D], name string) {
	s.cookbook.init()
	s._menu = p
	s.self = group
	s.name = name
	s.instance = group
	s.concurrentLimit = new(int32)
	s.running = new(int32)
	s.spinLocker = &sync.Mutex{}
	s.runningLock = &sync.Mutex{}
	if parent != nil {
		s.parentSet = parent.tree()
	}
	s.nodes = iterateStruct(group, p, group, p.cookware())
}

func (s *SetBase[D]) OverridePath(path string) *SetBase[D] {
	s.path = &path
	return s
}

func (s SetBase[D]) Menu() IMenu {
	return s._menu
}

func (s SetBase[D]) menu() iMenu[D] {
	return s._menu
}

func (s SetBase[D]) Name() string {
	if s.path != nil {
		return *s.path
	}
	return s.name
}

func (s SetBase[D]) tree() []iSet[D] {
	return append([]iSet[D]{s.self}, s.parentSet...)
}

func (s SetBase[D]) Tree() []ISet {
	nodes := s.tree()
	res := make([]ISet, len(nodes))
	for i, node := range nodes {
		res[i] = node
	}
	return res
}
