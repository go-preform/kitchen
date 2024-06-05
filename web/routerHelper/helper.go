package routerHelper

import (
	"context"
	"github.com/go-preform/kitchen"
	"io"
	"net/http"
	"net/url"
)

type (
	// IWebParsableInput is an interface for cooker input enable parsing web request to input
	IWebParsableInput interface {
		ParseRequestToInput(kitchen.IWebBundle) (raw []byte, err error)
	}
	// IWebWrapper is an interface for web router wrapper
	IWebWrapper interface {
		FormatUrlParam(name string) string
		AddMenuToRouter(instance kitchen.IInstance, prefix ...string)
	}
	// IWebCookware is an interface for cookware that can be parsed from web request
	IWebCookware interface {
		kitchen.ICookware
		RequestParser(action kitchen.IDish, bundle kitchen.IWebBundle) (IWebCookware, error) //parse user, permission
	}
)

type DefaultWebBundle struct {
	readBodyErr error
	requestBody []byte
	request     *http.Request
	response    http.ResponseWriter
}

func NewDefaultWebBundle(request *http.Request, response http.ResponseWriter) DefaultWebBundle {
	return DefaultWebBundle{request: request, response: response}
}

func (d DefaultWebBundle) Ctx() context.Context {
	return d.request.Context()
}

func (d DefaultWebBundle) Method() string {
	return d.request.Method
}

func (d DefaultWebBundle) Body() ([]byte, error) {
	if d.requestBody == nil {
		d.requestBody, d.readBodyErr = io.ReadAll(d.request.Body)
	}
	return d.requestBody, d.readBodyErr
}

func (d DefaultWebBundle) Url() *url.URL {
	return d.request.URL
}

func (d DefaultWebBundle) UrlParams() map[string]string {
	return map[string]string{}
}

func (d DefaultWebBundle) Headers() http.Header {
	return d.request.Header
}

func (d DefaultWebBundle) Raw() any {
	return d.request
}

func (d DefaultWebBundle) Response() http.ResponseWriter {
	return d.response
}
