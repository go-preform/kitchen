package routerHelper

import (
	"fmt"
	"github.com/go-preform/kitchen"
	"github.com/iancoleman/strcase"
	"reflect"
	"strings"
)

func ContainsValueInSlice(slice []string, value string) bool {
	for _, v := range slice {
		if strcase.ToCamel(v) == strcase.ToCamel(value) {
			return true
		}
	}
	return false
}

type nameAndPos struct {
	name string
	pos  int
}

func DefaultUrlParamWrapper(name string) string {
	return "{" + name + "}"
}

func DishUrlAndMethod(dish kitchen.IDish, urlParamWrapper func(string) string) (url string, urlParams [][2]string, method string, webUrlParamMap []int) {
	var (
		input any
		param string
		tags  = dish.Tags()
	)
	method = tags.Get("method")
	input = dish.Input()
	switch input.(type) {
	case string:
		urlParams = append(urlParams, [2]string{urlParamWrapper("p1"), ""})
		method = setValueIfEmpty(method, "GET")
	default:
		iType := reflect.TypeOf(input)
		if iType != nil {
			if iType.Kind() == reflect.Ptr {
				iType = iType.Elem()
			}
			if iType.Kind() == reflect.Struct {
				for i := 0; i < iType.NumField(); i++ {
					if param = iType.Field(i).Tag.Get("urlParam"); param != "" {
						urlParams = append(urlParams, [2]string{fmt.Sprintf("{%s}", param), iType.Field(i).Tag.Get("desc")})
						webUrlParamMap = append(webUrlParamMap, i)
					}
				}
				if len(webUrlParamMap) == iType.NumField() {
					method = setValueIfEmpty(method, "GET")
				}
			}
			method = setValueIfEmpty(method, "POST")
		} else {
			method = setValueIfEmpty(method, "GET")
		}
	}
	if tags.Get("urlParams") != "" {
		urlParams = nil
		toAdd := strings.Split(tags.Get("urlParams"), ",")
		if tags.Get("urlParamDescs") != "" {
			descs := strings.Split(tags.Get("urlParamDescs"), ",")
			for i, v := range toAdd {
				urlParams = append(urlParams, [2]string{urlParamWrapper(v), descs[i]})
			}
		} else {
			for _, v := range toAdd {
				urlParams = append(urlParams, [2]string{urlParamWrapper(v), ""})
			}
		}
	}
	switch dish.Name() {
	case "GET", "HEAD", "OPTIONS", "PATCH", "TRACE", "CONNECT", "POST", "UPDATE", "DELETE", "PUT":
		method = dish.Name()
	default:
		if _, ok := tags.Lookup("path"); ok {
			url = tags.Get("path")
		} else {
			url = strcase.ToSnake(dish.Name())
		}
	}
	if plAction, ok := dish.(kitchen.IPipelineAction); ok {
		pk := reflect.TypeOf(plAction.Pipeline().NewModel().PrimaryKey())
		if pk != nil {
			if pk.Kind() == reflect.Array || pk.Kind() == reflect.Slice {
				for i := 0; i < pk.Len(); i++ {
					urlParams = append(urlParams, [2]string{urlParamWrapper(fmt.Sprintf("model_id_%d", i)), "Model ID"})
				}
			} else if pk.Kind() == reflect.Int {
				urlParams = append(urlParams, [2]string{urlParamWrapper("model_id"), "Model ID"})
			}

		}
	}
	return url, urlParams, method, webUrlParamMap
}

func Ternary[T any](condition bool, trueVal, falseVal T) T {
	if condition {
		return trueVal
	}
	return falseVal
}

func setValueIfEmpty(method, value string) string {
	method = strings.Trim(method, " ")
	/*if len(method) == 0 {
		method = value
	}
	return strings.ToUpper(method)*/
	return strings.ToUpper(Ternary[string](len(method) > 0, method, value))
}
