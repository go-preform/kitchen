package echoHelper

import (
	"context"
	"github.com/go-preform/kitchen"
	"github.com/go-preform/kitchen/web/routerHelper"
	"github.com/iancoleman/strcase"
	"github.com/labstack/echo"
	"net/http"
	"net/url"
	"strings"
)

type wrapper struct {
	router *echo.Router
}

func (m *wrapper) FormatUrlParam(name string) string {
	return routerHelper.DefaultUrlParamWrapper(name)
}

func (m *wrapper) AddMenuToRouter(instance kitchen.IInstance, prefix ...string) routerHelper.IWebWrapper {
	var (
		method   string
		urlParts []string
		handler  echo.HandlerFunc
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
		for _, method := range strings.Split(method, ",") {
			m.router.Add(method, strings.ReplaceAll("/"+strings.Join(urlParts, "/"), "//", "/"), handler)
		}
	} else {
		for _, node := range instance.Nodes() {
			if action, ok = any(node).(kitchen.IDish); ok {
				method, urlParts, handler = m.serveHttp(action)
				urlParts = append(prefix, urlParts...)
				for _, method := range strings.Split(method, ",") {
					m.router.Add(method, strings.ReplaceAll("/"+strings.Join(urlParts, "/"), "//", "/"), handler)
				}
			} else if group, ok = any(node).(kitchen.ISet); ok {
				m.AddMenuToRouter(group, append(prefix, strcase.ToSnake(group.Name()))...)
			}
		}
	}
	return m
}

func (m *wrapper) serveHttp(dish kitchen.IDish) (method string, urlParts []string, handler echo.HandlerFunc) {
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
		return method, urlParts, func(ctx echo.Context) error {
			var err error
			input := dish.Input()
			ww := &routerHelper.WebWriter{ResponseWriter: ctx.Response()}
			bundle := &webBundle{Context: ctx, writer: ww, paramNames: urlParamsN}
			input, _, err = routerHelper.ParseRequestToInput(input, bundle, webUrlParamMap, isWebInput)
			if err != nil {
				routerHelper.WebReturn(dish, ww, nil, routerHelper.WebErr{Err: err, HttpStatus: http.StatusBadRequest, ErrorCode: http.StatusBadRequest})
				return err
			}
			if isWebCookware {
				cookware, err = any(cookware).(routerHelper.IWebCookware).RequestParser(dish, bundle)
				if err != nil {
					routerHelper.WebReturn(dish, ww, nil, routerHelper.WebErr{Err: err, HttpStatus: http.StatusBadRequest, ErrorCode: http.StatusBadRequest})
					return err
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

			output, err := plAction.ExecByIdAny(kitchen.NewWebContext(bundle.Ctx(), bundle, cookware), input, ids...)

			routerHelper.WebReturn(dish, ww, output, err)
			return nil
		}
	} else {
		return method, urlParts, func(ctx echo.Context) error {
			var err error
			input := dish.Input()
			ww := &routerHelper.WebWriter{ResponseWriter: ctx.Response()}
			bundle := &webBundle{Context: ctx, writer: ww, paramNames: urlParamsN}
			input, _, err = routerHelper.ParseRequestToInput(input, bundle, webUrlParamMap, isWebInput)
			if err != nil {
				routerHelper.WebReturn(dish, ww, nil, routerHelper.WebErr{Err: err, HttpStatus: http.StatusBadRequest, ErrorCode: http.StatusBadRequest})
				return err
			}
			if isWebCookware {
				cookware, err = any(cookware).(routerHelper.IWebCookware).RequestParser(dish, bundle)
				if err != nil {
					routerHelper.WebReturn(dish, ww, nil, routerHelper.WebErr{Err: err, HttpStatus: http.StatusBadRequest, ErrorCode: http.StatusBadRequest})
					return err
				}
			}
			output, err := dish.CookAny(kitchen.NewWebContext(bundle.Ctx(), bundle, cookware), input)

			routerHelper.WebReturn(dish, ww, output, err)
			return nil
		}
	}
}

func NewWrapper(router *echo.Router) routerHelper.IWebWrapper {
	return &wrapper{
		router: router,
	}
}

type webBundle struct {
	echo.Context
	writer     http.ResponseWriter
	paramNames []string
	params     map[string]string
}

func (w webBundle) Ctx() context.Context {
	return w.Context.Request().Context()
}

func (w webBundle) Method() string {
	return w.Method()
}

func (w *webBundle) Body() ([]byte, error) {
	return w.Body()
}

func (w *webBundle) Url() *url.URL {
	return w.Url()
}

func (w *webBundle) UrlParams() map[string]string {
	if w.params == nil {
		w.params = map[string]string{}
		for _, p := range w.paramNames {
			w.params[p] = w.Param(p)
		}
	}
	return w.params
}

func (w webBundle) Headers() http.Header {
	return w.Request().Header
}

func (w webBundle) Raw() any {
	return w.Context
}

func (w webBundle) Response() http.ResponseWriter {
	return w.Context.Response()
}
