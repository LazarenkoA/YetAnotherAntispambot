package main

import (
	app "Antispam"
	"context"
	"github.com/alecthomas/kingpin/v2"
)

type wrapper struct {
	settings map[string]string
	err      error
}

var (
	kp    *kingpin.Application
	debug bool
)

func init() {
	kp = kingpin.New("Антиспам бот", "")
	kp.Flag("debug", "вывод отладочной информации").Short('d').BoolVar(&debug)
}

func main() {
	if err := app.Run(context.Background()); err != nil {
		panic(err)
	}
}
