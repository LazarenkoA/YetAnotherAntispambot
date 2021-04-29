package main

import (
	dry "github.com/ungerik/go-dry"
	"gopkg.in/yaml.v2"
)

type answer struct {
	Txt     string `yaml:"txt"`
	Correct bool   `yaml:"correct"`
}

type question struct {
	Txt string `yaml:"txt"`
	Img string `yaml:"img"`
}

type Conf struct {
	// Обратный отчет в секундах
	Timeout     int    `yaml:"timeout"`
	KickCaption string `yaml:"kickCaption"`

	Question question  `yaml:"question"`
	Answers  []*answer `yaml:"answers"`
}

func LoadConfFromFile(confpath string) (result *Conf, err error) {
	if b, err := dry.FileGetBytes(confpath); err == nil {
		return LoadConf(b)
	} else {
		return nil, err
	}
}

func LoadConf(conf []byte) (result *Conf, err error) {
	result = new(Conf)
	err = yaml.Unmarshal(conf, &result)
	return result, err
}
