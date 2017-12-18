package exporter

import (
	"fmt"
	"github.com/fstab/grok_exporter/config/v2"
)

type Mutator interface {
	apply(labels *map[string]string)
}

type GsubInput struct {
	label       string
	replacement string
	regex       *OnigurumaRegexp
}

type GsubMutator struct {
	Mutator
	inputs []GsubInput
}

func (m GsubMutator) apply(labels *map[string]string) {
	for _, input := range m.inputs {
		label := input.label
		value := (*labels)[label]
		(*labels)[label] = input.regex.ReplaceAll(value, input.replacement)
	}
}

func createMutator(config v2.MutateConfig) Mutator {
	libonig, err := InitOnigurumaLib()
	if err != nil {
		fmt.Errorf("mutate error: %v", err.Error())
	}

	if config.Filter == "gsub" {
		array := config.Input.Array
		inputs := make([]GsubInput, 0, len(array)/2+1)
		for i := 0; i+2 <= len(array); i += 3 {
			regex, err := libonig.Compile(array[i+1])
			if err != nil {
				fmt.Errorf("mutate error: %v", err.Error())
			}
			inputs = append(inputs, GsubInput{
				label:       array[i],
				replacement: array[i+2],
				regex:       regex,
			})
		}
		return GsubMutator{
			inputs: inputs,
		}
	} else {
		fmt.Errorf("Unsupport mutator '%s'", config.Filter)
	}
	return nil
}

func CreateMutators(configs []v2.MutateConfig) []Mutator {
	mutators := make([]Mutator, len(configs))
	for i, config := range configs {
		if mutator := createMutator(config); mutator != nil {
			mutators[i] = mutator
		}
	}
	return mutators
}
