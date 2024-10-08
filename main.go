package main

import (
	"context"
	"fmt"
	"github.com/alecthomas/kingpin/v2"
	"github.com/pkg/errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
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

const (
	questionsKey = "questions"
	lastMsgKey   = "lastMsg"
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

	wd := new(Telega)
	wdUpdate, err := wd.New(debug, cert)
	if err != nil {
		fmt.Println("create telegrtam client error:\n", err.Error())
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go shutdown(wd, cancel)

	for {
		var update tgbotapi.Update

		select {
		case <-ctx.Done():
			return
		case update = <-wdUpdate:
		}

		msg := wd.GetMessage(update)
		if msg == nil {
			continue
		}

		chatID := msg.Chat.ID
		me, _ := wd.bot.GetMe()
		if p := strings.Split(msg.Text, "@"); msg.Text != "" && msg.Text[0] == '/' && len(p) == 2 && p[1] != me.UserName {
			continue
		}

		// обработка команд кнопок
		if wd.CallbackQuery(update) {
			continue
		}

		command := wd.GetMessage(update).Command()
		switch command {
		case "start":
			txt := fmt.Sprintf("Привет %s %s\n"+
				"Что бы начать пользоваться ботом нужно выполнить следующие действия:\n"+
				"1. Добавить бота в нужную группу\n"+
				"2. Выдать боту админские права\n"+
				"3. Выполнить в боте команду /configuration, выбрать чат и загрузить (отправить файл) конфиг. "+
				"Пример конфига можно скачать выполнив команду /exampleconf", msg.From.FirstName, msg.From.LastName)
			wd.SendMsg(txt, "", chatID, Buttons{})
			continue
		case "configuration":
			key := strconv.Itoa(wd.GetMessage(update).From.ID)
			if wd.r.KeyExists(key) {
				configuration(wd, update, chatID)
			} else {
				wd.SendMsg("Для вас не найден активный чат, видимо вы не добавили бота в чат.", "", chatID, Buttons{})
			}
			continue
		case "exampleconf":
			if f, err := os.CreateTemp("", "*.yaml"); err == nil {
				f.WriteString(confExample())
				f.Close()

				wd.SendFile(chatID, f.Name())
				os.RemoveAll(f.Name())
			}
			continue
		case "test":
			// для теста
			msg.NewChatMembers = &[]tgbotapi.User{
				{ID: 22, FirstName: "test", LastName: "test", UserName: "test", LanguageCode: "", IsBot: false},
			}
			continue
		case "clearLastMsg":
			wd.deleteLastMsg(msg.From.ID)
		case "allchats":
			fmt.Println(strings.Join(wd.getAllChats(), "\n"))
		case "help":
			msg := fmt.Sprintf("Антиспам для групп, при входе нового участника в группу бот задает вопрос, если ответа нет, участник блокируется.\n"+
				"Если нужно заблокировать пользователя тегните сообщения ботом (ответить на сообщение с текстом @%s).", me.UserName)
			wd.SendMsg(msg, "", chatID, Buttons{})
			continue
		default:
			if command != "" {
				fmt.Printf("Команда %s не поддерживается\n", command)
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

		if msg.NewChatMembers != nil {
			for _, user := range *msg.NewChatMembers {
				handlerAddNewMembers(wd, update, user, readConf(wd, chatID))
			}

			continue
		}

		//if msg.LeftChatMember != nil {
		//	fmt.Println("-----", msg.LeftChatMember.ID)
		//	wd.deleteLastMsg(msg.LeftChatMember.ID)
		//	continue
		//}

		if msg.LeftChatMember != nil && msg.LeftChatMember.ID == me.ID {
			wd.r.DeleteItems(strconv.Itoa(wd.GetMessage(update).From.ID), strconv.FormatInt(chatID, 10))
			continue
		}

		// вызов кворума на бан
		if strings.Contains(msg.Text, "@"+strings.TrimSpace(me.UserName)) && msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
			conf := readConf(wd, chatID)
			wd.StartVoting(msg, chatID, conf.CountVoted)
		}

		user := wd.GetMessage(update).From
		if txt := wd.GetMessage(update).Text; txt != "" {
			// если это первое сообщение анализируем его на спам
			if s, r := wd.IsSPAM(user.ID, txt, readConf(wd, chatID)); s {
				wd.deleteSpam(user, r, wd.GetMessage(update).MessageID, chatID)
			}
		}
	}
}

func configuration(wd *Telega, update tgbotapi.Update, chatID int64) {
	buttons := Buttons{}
	chats, _ := wd.r.Items(strconv.Itoa(wd.GetMessage(update).From.ID))
	key := strconv.FormatInt(chatID, 10)

	var msg *tgbotapi.Message
	handler := func(_ *tgbotapi.Update, b *Button) bool {
		chat := b.ID
		buttons := Buttons{
			&Button{
				caption: "Скачать",
				handler: func(*tgbotapi.Update, *Button) bool {
					if !getSettings(wd, chat, chatID) {
						wd.EditMsg(msg, "Для данного чата не найдено настроек", buttons)
					}
					return false
				},
			},
		}

		wd.EditMsg(msg, "Что бы загрузить настройки отправьте файл <b>yaml</b>\n"+
			"<pre>Пример настроек можно посмотреть в репазитории https://github.com/LazarenkoA/YetAnotherAntispambot</pre>\n"+
			"Что бы скачать текущие настройки нажмите \"Скачать\"", buttons)

		wd.hooks[key] = func(upd tgbotapi.Update) bool {
			if upd.Message == nil {
				return false
			}
			if confdata, err := wd.ReadFile(upd.Message); err == nil {
				settings, err := wrap(wd.r.StringMap(questionsKey)).result()
				if err != nil {
					wd.SendMsg(fmt.Sprintf("Произошла ошибка при получении значения из redis:\n%v", err), "", chatID, Buttons{})
				}

				settings[chat] = confdata
				wd.r.SetMap(questionsKey, settings)

				return true
			} else {
				return false
			}
		}

		return false
	}

	for _, chat := range chats {
		caption, err := wd.r.Get(chat)
		if err != nil || caption == "" {
			caption = chat
		}
		buttons = append(buttons, &Button{
			caption: caption,
			handler: handler,
			ID:      chat,
		})
	}

	msg, _ = wd.SendMsg("выберите чат", "", chatID, buttons)
}

func getSettings(wd *Telega, key string, chatID int64) bool {
	settings, err := wrap(wd.r.StringMap(questionsKey)).result()
	if err != nil {
		log.Println(fmt.Errorf("ошибка получения конфига из redis: %w", err))
	}

	if s, ok := settings[key]; ok {
		if f, err := os.CreateTemp("", "*.yaml"); err == nil {
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

func handlerAddNewMembers(wd *Telega, update tgbotapi.Update, appendedUser tgbotapi.User, conf *Conf) {
	var message *tgbotapi.Message

	chat := wd.GetMessage(update).Chat
	parentMsgID := wd.GetMessage(update).MessageID

	// когда добавили бота в чат, проверяем является ли он админом, если нет, сообщаем что нужно добавить в группу
	me, _ := wd.bot.GetMe()
	if appendedUser.ID == me.ID {
		if !wd.MeIsAdmin(chat.ChatConfig()) {
			message, _ := wd.SendMsg("Для корректной работы сделайте меня администратором", "", chat.ID, Buttons{})

			// отслеживаем статус, когда сделают администратором удаляем сообщение
			go func() {
				tick := time.NewTicker(time.Second)
				defer tick.Stop()

				for range tick.C {
					if wd.MeIsAdmin(chat.ChatConfig()) {
						wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
							ChatID:    chat.ID,
							MessageID: message.MessageID})
						break
					}
				}
			}()

			strChatID := strconv.FormatInt(chat.ID, 10)
			wd.r.AppendItems(strconv.Itoa(wd.GetMessage(update).From.ID), strChatID)
			wd.r.Set(strChatID, chat.Title, -1)
		}
		return
	}

	if conf == nil {
		log.Printf("для чата %s %s (%s) не определены настройки\n", chat.FirstName, chat.LastName, chat.UserName)
		return
	}

	//if true {
	//	wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
	//		ChatID:    chat.ID,
	//		MessageID: parentMsgID})
	//	return
	//}

	wd.DisableSendMessages(chat.ID, &appendedUser, 0) // ограничиваем пользователя писать сообщения пока он не ответит верно на вопрос

	handlers := []func(*tgbotapi.Update, *Button) bool{}
	deleteMessage := func() {
		if _, err := wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
			ChatID:    wd.GetMessage(update).Chat.ID,
			MessageID: message.MessageID}); err == nil {
			wd.r.DeleteItems(keyActiveMSG, strconv.Itoa(message.MessageID))
		}
	}
	handlercancel := func(update *tgbotapi.Update, _ *Button) (result bool) {
		from := wd.GetUser(update)
		if result = update == nil || from.ID == appendedUser.ID || wd.UserIsAdmin(chat.ChatConfig(), from); result {
			deleteMessage()
			wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
				ChatID:    chat.ID,
				MessageID: parentMsgID})
			wd.KickChatMember(appendedUser, tgbotapi.KickChatMemberConfig{
				ChatMemberConfig: tgbotapi.ChatMemberConfig{
					ChatID:             chat.ID,
					SuperGroupUsername: "",
					ChannelUsername:    "",
					UserID:             appendedUser.ID,
				},
				UntilDate: 0,
			})
		}
		return result
	}

	ctx, cancel := context.WithCancel(context.Background())

	b := Buttons{}
	for _, ans := range conf.Answers {
		a := ans // для замыкания
		handlers = append(handlers, func(update *tgbotapi.Update, currentButton *Button) (result bool) {
			from := wd.GetUser(update)
			if result = from.ID == appendedUser.ID || wd.UserIsAdmin(chat.ChatConfig(), from); result {
				if a.Correct {
					cancel()
					wd.EnableWritingMessages(chat.ID, &appendedUser)
					deleteMessage()
				} else {
					deleteMessage()
					wd.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
						ChatID:    chat.ID,
						MessageID: parentMsgID})
					wd.KickChatMember(appendedUser, tgbotapi.KickChatMemberConfig{
						ChatMemberConfig: tgbotapi.ChatMemberConfig{
							ChatID:             chat.ID,
							SuperGroupUsername: "",
							ChannelUsername:    "",
							UserID:             appendedUser.ID,
						},
						UntilDate: 0,
					})
				}
			} else {
				add := "к тому же вы ответили не верно."
				if a.Correct {
					add = "но вы ответили верно, молодцом!"
				}
				wd.bot.AnswerCallbackQuery(tgbotapi.CallbackConfig{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "Вопрос не для вас, " + add,
					ShowAlert:       true,
				})
			}
			return result
		})

		b = append(b, &Button{
			caption: ans.Txt,
			handler: handlers[len(handlers)-1],
		})
	}

	caption := "Не знаю"
	timeout := 60
	if conf.KickCaption != "" {
		caption = conf.KickCaption
	}
	if conf.Timeout > 0 {
		timeout = conf.Timeout
	}
	b = append(b, &Button{
		caption: caption,
		handler: handlercancel,
		timer:   timeout,
	})

	txt := fmt.Sprintf("Привет %s %s\nДля проверки на антиспам просьба ответить на вопрос:"+
		"\n%s", appendedUser.FirstName, appendedUser.LastName, conf.Question.Txt)
	message, _ = wd.ReplyMsg(txt, conf.Question.Img, chat.ID, b, wd.GetMessage(update).MessageID)
	wd.r.AppendItems(keyActiveMSG, strconv.Itoa(message.MessageID))

	// вместо таймера на кнопке
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second * time.Duration(timeout)):
			handlercancel(&update, nil)
			cancel()
		}
	}()
}

func readConf(wd *Telega, chatID int64) *Conf {
	settings, err := wrap(wd.r.StringMap(questionsKey)).result() // todo переделать на мапу в памяти как lastMsg
	if err != nil {
		log.Println(errors.Wrap(err, "read settings error"))
		return nil
	}

	key := strconv.FormatInt(chatID, 10)
	confStr, ok := settings[key]
	if !ok {
		return nil
	}

	conf, err := LoadConf([]byte(confStr))
	if err != nil {
		return nil
	}
	return conf
}

func wrap(settings map[string]string, err error) *wrapper {
	return &wrapper{
		settings: settings,
		err:      err,
	}
}

func shutdown(wd *Telega, cancel context.CancelFunc) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	log.Println("Shutting down")
	wd.Shutdown()
	cancel()
}

func (w *wrapper) result() (map[string]string, error) {
	if w.err == nil {
		return w.settings, nil
	} else {
		return map[string]string{}, w.err
	}
}
