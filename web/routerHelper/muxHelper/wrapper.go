package muxHelper

import (
	"github.com/go-preform/kitchen"
	"github.com/go-preform/kitchen/web/routerHelper"
	"github.com/gorilla/mux"
	"github.com/iancoleman/strcase"
	"net/http"
	"strings"
)

type wrapper struct {
	router *mux.Router
}

type muxBundle struct {
	routerHelper.DefaultWebBundle
}

func (b muxBundle) UrlParams() map[string]string {
	return mux.Vars(b.Raw().(*http.Request))
}

func (m *wrapper) FormatUrlParam(name string) string {
	return routerHelper.DefaultUrlParamWrapper(name)
}

func (m *wrapper) AddMenuToRouter(instance kitchen.IInstance, prefix ...string) routerHelper.IWebWrapper {
	var (
		method   string
		urlParts []string
		handler  http.HandlerFunc
		ok       bool
		group    kitchen.ISet
		action   kitchen.IDish
	)
	if len(prefix) != 0 {
		if prefix[0] == "" {
			prefix = prefix[1:]
		} else if strings.HasPrefix(prefix[0], "/") {
			prefix[0] = prefix[0][1:]
		}
	}
	if action, ok = any(instance).(kitchen.IDish); ok {
		method, urlParts, handler = m.serveHttp(action)
		urlParts = append(prefix, urlParts...)
		m.router.HandleFunc(strings.ReplaceAll("/"+strings.Join(urlParts, "/"), "//", "/"), handler).Methods(method)
	} else {
		for _, node := range instance.Nodes() {
			if action, ok = any(node).(kitchen.IDish); ok {
				method, urlParts, handler = m.serveHttp(action)
				urlParts = append(prefix, urlParts...)
				m.router.HandleFunc(strings.ReplaceAll("/"+strings.Join(urlParts, "/"), "//", "/"), handler).Methods(method)
			} else if group, ok = any(node).(kitchen.ISet); ok {
				m.AddMenuToRouter(group, append(prefix, strcase.ToSnake(group.Name()))...)
			}
		}
	}
	return m
}

func (m *wrapper) serveHttp(dish kitchen.IDish) (method string, urlParts []string, handler http.HandlerFunc) {
	url, urlParams, method, webUrlParamMap := routerHelper.DishUrlAndMethod(dish, m.FormatUrlParam)
	input := dish.Input()
	cookware := dish.Cookware()
	_, isWebInput := input.(routerHelper.IWebParsableInput)
	_, isWebCookware := cookware.(routerHelper.IWebCookware)
	urlParamsN := make([]string, 0)
	for _, up := range urlParams {
		urlParamsN = append(urlParamsN, up[0])
	}
	if url != "" {
		urlParts = append([]string{url}, urlParamsN...)
	} else {
		urlParts = urlParamsN
	}
	if plAction, ok := dish.(kitchen.IPipelineAction); ok {
		return method, urlParts, func(w http.ResponseWriter, r *http.Request) {
			var err error
			input := dish.Input()
			ww := &routerHelper.WebWriter{ResponseWriter: w}
			bundle := &muxBundle{routerHelper.NewDefaultWebBundle(r, ww)}
			input, _, err = routerHelper.ParseRequestToInput(input, bundle, webUrlParamMap, isWebInput)
			if err != nil {
				routerHelper.WebReturn(dish, ww, nil, routerHelper.WebErr{Err: err, HttpStatus: http.StatusBadRequest, ErrorCode: http.StatusBadRequest})
				return
			}
			if isWebCookware {
				cookware, err = any(cookware).(routerHelper.IWebCookware).RequestParser(dish, bundle)
				if err != nil {
					routerHelper.WebReturn(dish, ww, nil, routerHelper.WebErr{Err: err, HttpStatus: http.StatusBadRequest, ErrorCode: http.StatusBadRequest})
					return
				}
			}
			var (
				ids    []any
				idV    string
				idName string
			)

			for idName, idV = range bundle.UrlParams() {
				if strings.HasPrefix(idName, "model_id_") {
					ids = append(ids, idV)
				}
			}

			output, err := plAction.ExecByIdAny(kitchen.NewWebContext(r.Context(), bundle, cookware), input, ids...)

			routerHelper.WebReturn(dish, ww, output, err)
		}
	} else {
		return method, urlParts, func(w http.ResponseWriter, r *http.Request) {
			var err error
			input := dish.Input()
			ww := &routerHelper.WebWriter{ResponseWriter: w}
			bundle := &muxBundle{routerHelper.NewDefaultWebBundle(r, ww)}
			input, _, err = routerHelper.ParseRequestToInput(input, bundle, webUrlParamMap, isWebInput)
			if err != nil {
				routerHelper.WebReturn(dish, ww, nil, routerHelper.WebErr{Err: err, HttpStatus: http.StatusBadRequest, ErrorCode: http.StatusBadRequest})
				return
			}
			if isWebCookware {
				cookware, err = any(cookware).(routerHelper.IWebCookware).RequestParser(dish, bundle)
				if err != nil {
					routerHelper.WebReturn(dish, ww, nil, routerHelper.WebErr{Err: err, HttpStatus: http.StatusBadRequest, ErrorCode: http.StatusBadRequest})
					return
				}
			}
			output, err := dish.CookAny(kitchen.NewWebContext(r.Context(), bundle, cookware), input)

			routerHelper.WebReturn(dish, ww, output, err)
		}
	}
}

func NewWrapper(router *mux.Router) routerHelper.IWebWrapper {
	return &wrapper{
		router: router,
	}
}
