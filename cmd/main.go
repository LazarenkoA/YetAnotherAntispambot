package main

import (
	app "Antispam"
	"context"
	"fmt"
	"github.com/alecthomas/kingpin/v2"
	"os"
)

type wrapper struct {
	settings map[string]string
	err      error
}

var (
	BotToken   = os.Getenv("BotToken")
	WebhookURL = os.Getenv("WebhookURL")
	port       = os.Getenv("PORT")
	redisaddr  = os.Getenv("REDIS")
	cert       = os.Getenv("CRT")
)

var (
	kp    *kingpin.Application
	debug bool
)

func init() {
	kp = kingpin.New("Антиспам бот", "")
	kp.Flag("debug", "вывод отладочной информации").Short('d').BoolVar(&debug)
}

func main() {
	if BotToken == "" {
		fmt.Println("в переменных окружения не задан BotToken")
		os.Exit(1)
	}
	if port == "" {
		fmt.Println("в переменных окружения не задан PORT")
		os.Exit(1)
	}
	if redisaddr == "" {
		fmt.Println("в переменных окружения не задан адрес redis")
		os.Exit(1)
	}

	if err := app.Run(context.Background()); err != nil {
		panic(err)
	}
}
