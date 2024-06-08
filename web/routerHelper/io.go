package routerHelper

import (
	"encoding/json"
	"fmt"
	"github.com/go-preform/kitchen"
	"google.golang.org/protobuf/proto"
	"net/http"
	"reflect"
	"strings"
)

type WebErr struct {
	Err        error
	HttpStatus int
	Extra      map[string]string
	ErrorCode  int32
}

func (w WebErr) Error() string {
	return w.Err.Error()
}

type WebWriter struct {
	http.ResponseWriter
	returnedStatus bool
}

func (w *WebWriter) WriteHeader(statusCode int) {
	if !w.returnedStatus {
		w.ResponseWriter.WriteHeader(statusCode)
		w.returnedStatus = true
	}
}

func (w *WebWriter) Write(data []byte) (int, error) {
	return w.ResponseWriter.Write(data)
}

func WebReturn(a kitchen.IDish, w http.ResponseWriter, outputAny any, err error) {
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		_, _ = w.Write([]byte(err.Error()))
		fmt.Println(err)
		return
	}
	if outputAny != nil {
		switch outputAny := outputAny.(type) {
		case string:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(outputAny))
		default:
			if msg, ok := outputAny.(proto.Message); ok {
				//todo wrap
				w.Header().Set("Content-Type", "application/octet-stream")
				data, _ := proto.Marshal(msg)
				_, _ = w.Write(data)
			} else {
				w.Header().Set("Content-Type", "application/json")
				data, err := json.Marshal(outputAny)
				if err != nil {
					fmt.Println("marshalling error:", err)
				}
				_, _ = w.Write(data)
			}
		}
	}
}

func ParseRequestToInput(input any, bundle kitchen.IWebBundle, webUrlParamMap []int, isWebInput bool) (processedInput any, raw []byte, err error) {
	if isWebInput {
		raw, err = input.(IWebParsableInput).ParseRequestToInput(bundle)
	} else {
		var (
			urlParam map[string]string
			iVal     reflect.Value
			l        int
		)
		if bundle.Method() == http.MethodGet || bundle.Method() == http.MethodDelete {
			urlParam = bundle.UrlParams()
			for k, v := range bundle.Url().Query() {
				urlParam[k] = v[0]
			}
			raw = []byte(bundle.Url().String())
			if len(urlParam) != 0 {
				switch input.(type) {
				case string:
					if v, ok := urlParam["p1"]; ok {
						processedInput = any(v)
						break
					}
					for _, v := range urlParam {
						processedInput = any(v)
						break
					}
					return
				default:
					raw, _ = json.Marshal(urlParam)
					err = json.Unmarshal(raw, input)
					processedInput = input
				}
			}
		} else {
			if raw, err = bundle.Body(); err == nil && len(raw) != 0 {
				if msg, ok := input.(proto.Message); ok {
					err = proto.Unmarshal(raw, msg)
					if err != nil && raw[0] == '{' {
						err = json.Unmarshal(raw, &input)
					}
				} else {
					err = json.Unmarshal(raw, &input)
				}
				processedInput = input
			} else {
				processedInput = input
			}
			if l = len(webUrlParamMap); l != 0 {
				var (
					params = strings.Split(bundle.Url().Path, "/")
				)
				params = params[len(params)-l:]
				iVal = reflect.ValueOf(processedInput)
				if iVal.Kind() == reflect.Ptr {
					iVal = iVal.Elem()
				}
				for i, p := range webUrlParamMap {
					if p != -1 {
						iVal.Field(p).SetString(params[i])
					}
				}
			}
		}
	}
	return
}
