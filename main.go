package main

import (
	"fmt"
	"os"
)

var (
	BotToken = os.Getenv("BotToken")
	WebhookURL = os.Getenv("WebhookURL")
	port = os.Getenv("PORT")
)

func main() {
	if BotToken == "" {
		fmt.Println("в переменных окружения не задан BotToken")
		os.Exit(1)
	}

	wd := new(Telega)
	wdUpdate, err := wd.New()
	if err != nil {
		fmt.Println("не удалось подключить бота, ошибка:\n", err.Error())
		os.Exit(1)
	}

	for update := range wdUpdate {
		// обработка команд кнопок
		if wd.CallbackQuery(update) {
			continue
		}
		if update.Message == nil {
			continue
		}

		command := update.Message.Command()
		chatID := update.Message.Chat.ID

		if !update.Message.Chat.IsGroup() {
			continue
		}


		switch command {
		case "start":

		default:
			if command != "" {
				wd.SendMsg("Команда " + command + " не поддерживается", chatID, Buttons{})
				continue
			}
		}
	}
}
