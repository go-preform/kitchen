package kitchen

import (
	"reflect"
)

// PipelineBase is a super set of MenuBase, should embed to all pipelines.
type PipelineBase[P iPipeline[D, M], D IPipelineCookware[M], M IPipelineModel] struct {
	MenuBase[P, D]
	StageByStatus map[string]iPipelineStage[D, M]
}

func InitPipeline[D IPipelineCookware[M], M IPipelineModel, P iPipeline[D, M]](pipelinePtr P, dep D) P {
	pipelinePtr.initPipeline(pipelinePtr, dep)
	return pipelinePtr
}

func (p *PipelineBase[P, D, M]) initPipeline(pipeline iPipeline[D, M], dep D) {
	pipeline.initWithoutFields(pipeline, dep)
	p.StageByStatus = map[string]iPipelineStage[D, M]{}
	p.nodes = iteratePipelineStruct[D, M](pipeline, pipeline, p.StageByStatus, nil, dep)
}

func iteratePipelineStruct[D IPipelineCookware[M], M IPipelineModel](s any, pipeline iPipeline[D, M], stageByStatus map[string]iPipelineStage[D, M], stage iPipelineStage[D, M], bundle D) []iCookbook[D] {
	var (
		ppValue   = reflect.ValueOf(s).Elem()
		ppType    = ppValue.Type()
		fieldType reflect.StructField
		nodes     []iCookbook[D]
		path      string
		ok        bool
	)

	for i, l := 0, ppType.NumField(); i < l; i++ {
		fieldType = ppType.Field(i)
		path = fieldType.Name
		if fieldType.IsExported() && !fieldType.Anonymous {
			node := ppValue.Field(i).Addr().Interface()
			if _, ok = node.(iPipelineAction[D, M]); ok {
				initPipelineAction(pipeline.(iCookbook[D]), node.(iPipelineAction[D, M]), path, fieldType.Tag)
				nodes = append(nodes, node.(iCookbook[D]))
			} else if _, ok = node.(iDish[D]); ok {
				initDish(pipeline.(iCookbook[D]), node.(iDish[D]), path, fieldType.Tag)
				nodes = append(nodes, node.(iCookbook[D]))
			} else if _, ok = node.(iSet[D]); ok {
				if _, ok = node.(iPipelineStage[D, M]); ok {
					initPipelineStage(pipeline, node.(iPipelineStage[D, M]), path)
					nodes = append(nodes, node.(iCookbook[D]))
					stageByStatus[string(node.(iPipelineStage[D, M]).Status())] = node.(iPipelineStage[D, M])
				} else {
					initSet(pipeline.(iMenu[D]), node.(iSet[D]), stage.(iSet[D]), path)
					nodes = append(nodes, node.(iCookbook[D]))
				}
			} else if fieldType.Type.Kind() == reflect.Struct {
				nodes = append(nodes, iteratePipelineStruct[D, M](ppValue.Field(i).Addr().Interface(), pipeline, stageByStatus, stage, bundle)...)
			}
		}
	}
	return nodes
}

// GetActionsForStatus returns the actions available for the status.
func (p *PipelineBase[P, D, M]) GetActionsForStatus(status string) []IPipelineAction {
	if stage, ok := p.StageByStatus[status]; ok {
		return stage.Actions()
	}
	return nil
}

// GetActionsForModel returns the actions available for the model.
func (p *PipelineBase[P, D, M]) GetActionsForModel(model any) (string, []IPipelineAction) {
	status := string(model.(IPipelineModel).GetStatus())
	return status, p.GetActionsForStatus(status)
}

func (p PipelineBase[P, D, M]) NewModel() IPipelineModel {
	var (
		m M
	)
	return m
}
