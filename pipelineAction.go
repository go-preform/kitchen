package kitchen

import (
	"context"
	"errors"
	"github.com/go-preform/kitchen/stringMap"
	"github.com/iancoleman/strcase"
	"reflect"
)

type PipelineActionInput[M IPipelineModel, I any] struct {
	Input  I
	Model  M
	Before map[string]string
	Status PipelineStatus
}

type PipelineActionOutput[M IPipelineModel, O any] struct {
	Output O
	Model  M
}

// A PipelineAction is a super set of Dish specific for managing workflow of stateful data model.
type PipelineAction[D IPipelineCookware[M], M IPipelineModel, I any, O any] struct {
	Dish[D, *PipelineActionInput[M, I], *PipelineActionOutput[M, O]]
	stage           iPipelineStage[D, M]
	nextStatus      PipelineStatus
	willCreateModel bool
	canMap          bool
	urlPath         string
	newInput        func() I
}

func initPipelineAction[D IPipelineCookware[M], M IPipelineModel](parent iCookbook[D], act iPipelineAction[D, M], name string, tags reflect.StructTag) {
	act.initAction(parent, act, name, tags)
}

// SetNextStage sets the next stage for the action.
func (p *PipelineAction[D, M, I, O]) SetNextStage(stage IPipelineStage) *PipelineAction[D, M, I, O] {
	p.nextStatus = stage.Status()
	return p
}
func (p PipelineAction[D, M, I, O]) Status() PipelineStatus {
	if p.stage == nil {
		return ""
	}
	return p.stage.Status()
}

// SetCooker sets the cooker for the action, will transform to func(i IContext[D], p *PipelineActionInput[M, I]) (output *PipelineActionOutput[M, O], err error).
func (p *PipelineAction[D, M, I, O]) SetCooker(cooker PipelineDishCooker[D, M, I, O]) *PipelineAction[D, M, I, O] {
	var (
		m M
	)
	_, p.canMap = any(m).(IPipelineModelCanMap)
	p.cooker = func(i IContext[D], p *PipelineActionInput[M, I]) (output *PipelineActionOutput[M, O], err error) {
		outputWrap := &PipelineActionOutput[M, O]{}
		outputWrap.Output, outputWrap.Model, err = cooker(i.(IPipelineContext[D, M]), p.Model, p.Input)
		return outputWrap, err
	}
	return p
}

// WillCreateModel returns true if the action will create a model, can input nil in Cook.
func (p *PipelineAction[D, M, I, O]) WillCreateModel() bool {
	return p.willCreateModel
}

func (p *PipelineAction[D, M, I, O]) CreateModel() *PipelineAction[D, M, I, O] {
	p.willCreateModel = true
	return p
}

func (p *PipelineAction[D, M, I, O]) initAction(parent iCookbook[D], act iPipelineAction[D, M], name string, fieldTag reflect.StructTag) {
	p.init(parent, act, name, fieldTag)
	p.stage, _ = any(parent).(iPipelineStage[D, M])
	p.urlPath = strcase.ToSnake(p.name)
	var (
		i     I
		iType = reflect.TypeOf(i)
	)
	if iType != nil && iType.Kind() == reflect.Ptr {
		p.newInput = func() I {
			return reflect.New(iType.Elem()).Interface().(I)
		}
	} else {
		p.newInput = func() I {
			var i I
			return i
		}
	}
}

var ErrInvalidStatus = errors.New("invalid pipeline status")

// ExecWithModel executes the action with the model and input.
func (p *PipelineAction[D, M, I, O]) ExecWithModel(ctx context.Context, model M, input I) (output O, err error) {
	return p.execWithModel(p.newCtx(ctx), model, input)
}

// ExecWithModelAndDep executes the action with the model, input and dependencies.
func (p *PipelineAction[D, M, I, O]) ExecWithModelAndDep(ctx context.Context, dep D, model M, input I) (output O, err error) {
	return p.execWithModel(p.newCtx(ctx, dep), model, input)
}

// ExecByIdAny executes the action with the input and model id.
func (p *PipelineAction[D, M, I, O]) ExecByIdAny(ctx context.Context, input any, ids ...any) (output any, err error) {
	return p.ExecById(ctx, input.(I), ids...)
}

func (p *PipelineAction[D, M, I, O]) execWithModel(ctx *Context[D], model M, input I) (output O, err error) {
	var (
		tx         IDbTx
		dep        = ctx.Cookware()
		oStatus    PipelineStatus
		modelIsNil = reflect.ValueOf(model).IsNil()
		inputWrap  = &PipelineActionInput[M, I]{Input: input, Model: model}
	)
	if modelIsNil && !p.willCreateModel {
		return output, errors.New("need model")
	}
	if !modelIsNil {
		oStatus = model.GetStatus()
		inputWrap.Before = p.ModelToMap(model)
		inputWrap.Status = oStatus
	}
	if p.stage != nil && oStatus != p.stage.Status() {
		return output, ErrInvalidStatus
	}
	tx, err = dep.BeginTx(ctx)
	if err != nil {
		return output, err
	}
	ctx.dish = p
	var outputWrap *PipelineActionOutput[M, O]
	outputWrap, err = p.Dish.cook(&PipelineContext[D, M]{
		Context: *ctx,
		tx:      tx,
	}, inputWrap, func(output *PipelineActionOutput[M, O], resErr error) error {
		if resErr == nil {
			if p.nextStatus != "" {
				output.Model.SetStatus(p.nextStatus)
			}
			resErr = dep.SaveModel(tx, output.Model, oStatus)
		}
		return resErr
	})
	output = outputWrap.Output
	err = dep.FinishTx(tx, err)
	return
}

// ModelToMap converts the model to map for logging.
func (p PipelineAction[D, M, I, O]) ModelToMap(model IPipelineModel) map[string]string {
	if p.canMap {
		return model.(IPipelineModelCanMap).ToMap()
	}
	return stringMap.FromStruct(model)
}

// ExecByIdAndDep executes the action with the dependencies and model id.
func (p *PipelineAction[D, M, I, O]) ExecByIdAndDep(ctx context.Context, dep D, input I, modelId ...any) (output O, err error) {
	var (
		model M
	)
	if len(modelId) != 0 {
		model, err = dep.GetModelById(ctx, modelId...)
		if err != nil {
			return output, err
		}
	} else if !p.willCreateModel {
		return output, errors.New("pk is empty")
	}
	return p.ExecWithModelAndDep(ctx, dep, model, input)
}

func (p *PipelineAction[D, M, I, O]) execById(ctx *Context[D], input I, modelId ...any) (output O, err error) {
	var (
		model M
	)
	if len(modelId) != 0 {
		model, err = ctx.Cookware().GetModelById(ctx, modelId...)
		if err != nil {
			return output, err
		}
	} else if !p.willCreateModel {
		return output, errors.New("pk is empty")
	}
	return p.execWithModel(ctx, model, input)
}

// ExecById executes the action with the input and model id.
func (p *PipelineAction[D, M, I, O]) ExecById(ctx context.Context, input I, modelId ...any) (output O, err error) {
	return p.execById(p.newCtx(ctx), input, modelId...)
}

func (p PipelineAction[D, M, I, O]) Pipeline() IPipeline {
	return p.stage.pipeline()
}

// PipelineActionInputToAny transforms the input to PipelineActionInput.
func PipelineActionInputToAny[M IPipelineModel](input any) PipelineActionInput[M, any] {
	var (
		iV = reflect.ValueOf(input)
		mV reflect.Value
	)
	if iV.Type().Kind() == reflect.Ptr {
		iV = iV.Elem()
	}
	mV = iV.Field(1)
	return PipelineActionInput[M, any]{
		Input:  iV.Field(0).Interface(),
		Model:  mV.Interface().(M),
		Before: iV.Field(2).Interface().(map[string]string),
	}
}

// PipelineActionOutputToAny transforms the output to PipelineActionOutput.
func PipelineActionOutputToAny[M IPipelineModel](output any) PipelineActionOutput[M, any] {
	var (
		iV = reflect.ValueOf(output)
		mV reflect.Value
	)
	if iV.Type().Kind() == reflect.Ptr {
		iV = iV.Elem()
	}
	mV = iV.Field(1)
	return PipelineActionOutput[M, any]{
		Output: iV.Field(0).Interface(),
		Model:  mV.Interface().(M),
	}
}
