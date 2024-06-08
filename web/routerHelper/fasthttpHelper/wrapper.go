package fasthttpHelper

import (
	"context"
	"github.com/fasthttp/router"
	"github.com/go-preform/kitchen"
	"github.com/go-preform/kitchen/web/routerHelper"
	"github.com/iancoleman/strcase"
	"github.com/valyala/fasthttp"
	"net/http"
	"net/url"
	"strings"
)

type wrapper struct {
	router *router.Router
}

func (m *wrapper) FormatUrlParam(name string) string {
	return routerHelper.DefaultUrlParamWrapper(name)
}

func (m *wrapper) AddMenuToRouter(instance kitchen.IInstance, prefix ...string) routerHelper.IWebWrapper {
	var (
		method   string
		urlParts []string
		handler  fasthttp.RequestHandler
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
	for _, node := range instance.Nodes() {
		if action, ok = any(node).(kitchen.IDish); ok {
			method, urlParts, handler = m.serveHttp(action)
			urlParts = append(prefix, urlParts...)
			m.router.Handle(method, strings.ReplaceAll("/"+strings.Join(urlParts, "/"), "//", "/"), handler)
		} else if group, ok = any(node).(kitchen.ISet); ok {
			m.AddMenuToRouter(group, append(prefix, strcase.ToSnake(group.Name()))...)
		}
	}
	return m
}

func (m *wrapper) serveHttp(dish kitchen.IDish) (method string, urlParts []string, handler fasthttp.RequestHandler) {
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
		return method, urlParts, func(ctx *fasthttp.RequestCtx) {
			var err error
			input := dish.Input()
			ww := &routerHelper.WebWriter{ResponseWriter: &writerWrap{ctx}}
			bundle := &webBundle{RequestCtx: ctx, writer: ww, paramNames: urlParamsN}
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

			output, err := plAction.ExecByIdAny(kitchen.NewWebContext(ctx, bundle, cookware), input, ids...)

			routerHelper.WebReturn(dish, ww, output, err)
		}
	} else {
		return method, urlParts, func(ctx *fasthttp.RequestCtx) {
			var err error
			input := dish.Input()
			ww := &routerHelper.WebWriter{ResponseWriter: &writerWrap{ctx}}
			bundle := &webBundle{RequestCtx: ctx, writer: ww, paramNames: urlParamsN}
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
			output, err := dish.CookAny(kitchen.NewWebContext(ctx, bundle, cookware), input)

			routerHelper.WebReturn(dish, ww, output, err)
		}
	}
}

func NewWrapper(router *router.Router) routerHelper.IWebWrapper {
	return &wrapper{
		router: router,
	}
}

type webBundle struct {
	*fasthttp.RequestCtx
	writer     http.ResponseWriter
	url        *url.URL
	paramNames []string
	params     map[string]string
}

func (w webBundle) Ctx() context.Context {
	return w.RequestCtx
}

func (w webBundle) Method() string {
	return string(w.RequestCtx.Method())
}

func (w *webBundle) Body() ([]byte, error) {
	return w.RequestCtx.PostBody(), nil
}

func (w *webBundle) Url() *url.URL {
	if w.url == nil {
		uri := w.RequestCtx.URI()
		w.url = &url.URL{
			Scheme:      string(uri.Scheme()),
			User:        url.UserPassword(string(uri.Username()), string(uri.Password())),
			Host:        string(uri.Host()),
			Path:        string(uri.Path()),
			RawPath:     string(uri.PathOriginal()),
			RawQuery:    string(uri.QueryString()),
			RawFragment: string(uri.Hash()),
		}
	}
	return w.url
}

func (w *webBundle) UrlParams() map[string]string {
	if w.params == nil {
		w.params = map[string]string{}
		for _, p := range w.paramNames {
			w.params[p] = w.UserValue(p).(string)
		}
	}
	return w.params
}

// not supported please use Raw().(*fasthttp.RequestCtx).Header
func (w webBundle) Headers() http.Header {
	return nil
}

func (w webBundle) Raw() any {
	return w.RequestCtx
}

func (w webBundle) Response() http.ResponseWriter {
	return w.writer
}

type writerWrap struct {
	*fasthttp.RequestCtx
}

func (w *writerWrap) Header() http.Header {
	return make(http.Header)
}

func (w *writerWrap) Write(bytes []byte) (int, error) {
	return w.RequestCtx.Write(bytes)
}

func (w *writerWrap) WriteHeader(statusCode int) {
	w.SetStatusCode(statusCode)
}
