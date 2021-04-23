package main

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"io/ioutil"
	"os"
	"strconv"
	"time"
)

var (
	BotToken   = os.Getenv("BotToken")
	WebhookURL = os.Getenv("WebhookURL")
	port       = os.Getenv("PORT")
	redisaddr  = os.Getenv("REDIS")
)

const (
	questionskey = "questions"
)

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

	wd := new(Telega)
	wdUpdate, err := wd.New()
	if err != nil {
		fmt.Println("не удалось подключить бота, ошибка:\n", err.Error())
		os.Exit(1)
	}

	for update := range wdUpdate {
		msg := wd.GetMessage(update)
		if msg == nil {
			continue
		}

		chatID := msg.Chat.ID

		// обработка команд кнопок
		if wd.CallbackQuery(update) {
			continue
		}

		command := wd.GetMessage(update).Command()
		switch command {
		case "start":
			wd.SendMsg("👋🏻", chatID, Buttons{})
		case "configuration":
			key := strconv.Itoa(wd.GetMessage(update).From.ID)
			if wd.r.KeyExists(key) {
				configuration(wd, update, chatID)
			} else {
				wd.SendMsg("Для вас не найден активный чат", chatID, Buttons{})
			}
		default:
			if command != "" {
				wd.SendMsg("Команда "+command+" не поддерживается", chatID, Buttons{})
				continue
			} else {
				key := strconv.FormatInt(chatID, 10)
				if call, ok := wd.hooks[key]; ok {
					if call(update) {
						wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
							ChatID:    chatID,
							MessageID: msg.MessageID})

						delete(wd.hooks, key)
					}
				}
			}
		}

		newChatMembers := msg.NewChatMembers
		if newChatMembers != nil {
			for _, user := range *newChatMembers {
				handlerAddNewMembers(wd, update, user)
			}
		}
		me, _ := wd.bot.GetMe()
		if msg.LeftChatMember != nil && msg.LeftChatMember.ID == me.ID {
			wd.r.DeleteItems(strconv.Itoa(wd.GetMessage(update).From.ID), strconv.FormatInt(chatID, 10))
		}
	}
}

func configuration(wd *Telega, update tgbotapi.Update, chatID int64) {
	buttons := Buttons{}
	chats := wd.r.Items(strconv.Itoa(wd.GetMessage(update).From.ID))
	key := strconv.FormatInt(chatID, 10)

	for _, chat := range chats {
		handler := func(*tgbotapi.Update) bool { return true }
		buttons = append(buttons, &Button{
			caption: chat,
			handler: &handler,
		})
	}

	msg, _ := wd.SendMsg("выберите чат", chatID, buttons)
	for _, b := range buttons {
		chat := b.caption
		download := func(*tgbotapi.Update) bool { return true }

		*b.handler = func(*tgbotapi.Update) bool {
			buttons := Buttons{
				&Button{
					caption: "Скачать",
					handler: &download,
				},
			}

			msg := wd.EditMsg(msg, "Что бы загрузить настройки отправьте файл <b>yaml</b>\n"+
				"<pre>Пример настроек можно посмотреть в репазитории https://github.com/LazarenkoA/YetAnotherAntispambot</pre>\n"+
				"Что бы скачать текущие настройки нажмите \"Скачать\"", buttons)
			download = func(*tgbotapi.Update) bool {
				if !getSettings(wd, chat, chatID) {
					wd.EditMsg(msg, "Для данного чата не найдено настроек", buttons)
				} else {
					wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
						ChatID:    chatID,
						MessageID: msg.MessageID})
				}
				return true
			}

			wd.hooks[key] = func(upd tgbotapi.Update) bool {
				if upd.Message == nil {
					return false
				}
				if confdata, err := wd.ReadFile(upd.Message); err == nil {
					settings := wd.r.StringMap(questionskey)
					settings[chat] = confdata
					wd.r.SetMap(questionskey, settings)

					return true
				} else {
					return false
				}
			}

			return true
		}
	}
}

func getSettings(wd *Telega, key string, chatID int64) bool {
	settings := wd.r.StringMap(questionskey)
	if s, ok := settings[key]; ok {
		if f, err := ioutil.TempFile("", "*.yaml"); err == nil {
			defer os.RemoveAll(f.Name())
			f.WriteString(s)
			f.Close()

			if err := wd.SendFile(chatID, f.Name()); err != nil {
				return false
			}
		}
	} else {
		return false
	}
	return true
}

func handlerAddNewMembers(wd *Telega, update tgbotapi.Update, user tgbotapi.User) {
	chat := wd.GetMessage(update).Chat

	// когда добавили бота в чат, проверяем является ли он админом, если нет, сообщаем что нужно добавить в группу
	me, _ := wd.bot.GetMe()
	if user.ID == me.ID {
		if !wd.MeIsAdmin(chat.ChatConfig()) {
			message, _ := wd.SendMsg("Для корректной работы сделайте меня администратором", chat.ID, Buttons{})

			// отслеживаем статус, когда сделают администратором удаляем сообщение
			go func() {
				tick := time.NewTicker(time.Second)
				for range tick.C {
					if wd.MeIsAdmin(chat.ChatConfig()) {
						wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
							ChatID:    chat.ID,
							MessageID: message.MessageID})

						tick.Stop()
					}
				}
			}()

			wd.r.AppendItems(strconv.Itoa(wd.GetMessage(update).From.ID), strconv.FormatInt(chat.ID, 10))
		}
		return
	}

	settings := wd.r.StringMap(questionskey)
	key := strconv.FormatInt(chat.ID, 10)
	confStr, ok := settings[key]
	if !ok {
		return
	}

	conf, err := LoadConf([]byte(confStr))
	if err != nil {
		return
	}

	handlers := []func(*tgbotapi.Update) bool{}
	handlercancel := func(*tgbotapi.Update) bool { return true }
	deleteMessage := func() {}

	b := Buttons{}
	for _, ans := range conf.Answers {
		a := ans // для замыкания
		handlers = append(handlers, func(update *tgbotapi.Update) (result bool) {
			from := wd.GetUser(update)
			if result = from.ID == user.ID; result {
				if a.Correct {
					deleteMessage()
				} else {
					deleteMessage()
					wd.bot.KickChatMember(tgbotapi.KickChatMemberConfig{
						ChatMemberConfig: tgbotapi.ChatMemberConfig{
							ChatID:             wd.GetMessage(*update).Chat.ID,
							SuperGroupUsername: "",
							ChannelUsername:    "",
							UserID:             user.ID,
						},
						UntilDate: 0,
					})
				}
			}
			return result
		})

		b = append(b, &Button{
			caption: ans.Txt,
			handler: &handlers[len(handlers)-1],
		})
	}
	b = append(b, &Button{
		caption: "Не знаю",
		handler: &handlercancel,
		timer:   conf.Timeout,
	})

	txt := fmt.Sprintf("Привет %s %s\nДля проверки на антиспам просьба ответить на вопрос:"+
		"\n%s", user.FirstName, user.LastName, conf.Question)
	message, _ := wd.SendMsg(txt, chat.ID, b)

	deleteMessage = func() {
		wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
			ChatID:    wd.GetMessage(update).Chat.ID,
			MessageID: message.MessageID})
	}

	handlercancel = func(update *tgbotapi.Update) (result bool) {
		from := wd.GetUser(update)
		if result = update == nil || from.ID == user.ID; result {
			wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
				ChatID:    chat.ID,
				MessageID: message.MessageID})
			wd.bot.KickChatMember(tgbotapi.KickChatMemberConfig{
				ChatMemberConfig: tgbotapi.ChatMemberConfig{
					ChatID:             chat.ID,
					SuperGroupUsername: "",
					ChannelUsername:    "",
					UserID:             user.ID,
				},
				UntilDate: 0,
			})
		}
		return result
	}
}
