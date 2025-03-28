package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alecthomas/kingpin/v2"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type wrapper struct {
	settings map[string]string
	err      error
}

var (
	botToken    = os.Getenv("BotToken")
	webhookURL  = os.Getenv("WebhookURL")
	port        = os.Getenv("PORT")
	redisAddr   = os.Getenv("REDIS")
	cert        = os.Getenv("CRT")
	pollingMode = os.Getenv("POLLING_MODE")
)

var (
	kp    *kingpin.Application
	debug bool
)

const (
	questionsKey = "questions"
	lastMsgKey   = "lastMsg"
	userInfo     = "userInfo"
	killedUsers  = "killedUsers"
)

func init() {
	kp = kingpin.New("Антиспам бот", "")
	kp.Flag("debug", "вывод отладочной информации").Short('d').BoolVar(&debug)
}

func Run(ctx_ context.Context) error {
	if botToken == "" {
		return errors.New("в переменных окружения не задан BotToken")
	}
	if port == "" {
		return errors.New("в переменных окружения не задан PORT")
	}
	if redisAddr == "" {
		return errors.New("в переменных окружения не задан адрес redis")
	}

	wd := new(Telega)
	wdUpdate, err := wd.New(debug, cert, pollingMode == "1")
	if err != nil {
		fmt.Println("create telegrtam client error:\n", err.Error())
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(ctx_)
	go shutdown(wd, cancel)

	for {
		var update tgbotapi.Update

		select {
		case <-ctx.Done():
			return nil
		case update = <-wdUpdate:
		}

		chatMember := lo.If(update.ChatMember != nil, update.ChatMember).Else(update.MyChatMember)
		if chatMember != nil && chatMember.NewChatMember.Status == "member" && chatMember.OldChatMember.Status != "restricted" && chatMember.OldChatMember.Status != "administrator" {
			handlerAddNewMembers(wd, chatMember.Chat, chatMember.NewChatMember.User, &chatMember.From, readConf(wd, chatMember.Chat.ID))
		}

		msg := wd.GetMessage(update)
		if msg == nil {
			continue
		}

		chatID := msg.Chat.ID
		wd.SaveMember(chatID, wd.GetUser(&update))

		// удаляем сообщения о вступлении в группу
		if len(msg.NewChatMembers) > 0 {
			wd.DeleteMessage(chatID, msg.MessageID)
			continue
		}

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
			key := strconv.FormatInt(wd.GetMessage(update).From.ID, 10)
			if wd.r.KeyExists(key) {
				configuration(wd, update, chatID)
			} else {
				wd.SendMsg("Для вас не найден активный чат, видимо вы не добавили бота в чат.", "", chatID, Buttons{})
			}
			continue
		case "random_moderator":
			if userName, deadline := wd.GetActiveRandModerator(chatID); userName != "" {
				wd.SendTTLMsg(fmt.Sprintf("%s уже выбран модератором, перевыбрать можно после %s", userName, deadline.Format("02-01-2006 15:04:05")), "", chatID, Buttons{}, time.Second*5)
				continue
			}

			randUser := wd.GetRandUser(chatID, msg.From.ID)
			if randUser == nil {
				wd.SendTTLMsg("Не смог получить кандидата", "", chatID, Buttons{}, time.Second*5)
				continue
			}

			if wd.UserIsAdmin(msg.Chat.ChatConfig(), randUser.ID) {
				wd.SendTTLMsg(fmt.Sprintf("%s уже является администратором, можно попробовать повторно выбрать кандидатуру", randUser.Name), "", chatID, Buttons{}, time.Second*5)
				continue
			}

			deadline := time.Now().Add(time.Hour * 24)
			if err := wd.AppointModerator(chatID, randUser, deadline); err != nil {
				wd.SendTTLMsg(fmt.Sprintf("Произошла ошибка: %v", err.Error()), "", chatID, Buttons{}, time.Second*5)
			} else {
				wd.SendTTLMsg(fmt.Sprintf("У нас новый модератор (%s), срок до %v", randUser.Name, deadline.Format("02-01-2006 15:04")), "", chatID, Buttons{}, time.Second*15)
			}
		case "russian_roulette":
			players := []*UserInfo{wd.CastUserToUserinfo(msg.From)}

			if wd.UserIsAdmin(msg.Chat.ChatConfig(), msg.From.ID) && !wd.UserIsCreator(msg.Chat.ChatConfig(), msg.From.ID) {
				wd.SendTTLMsg("Администраторы не могут играть", "", chatID, Buttons{}, time.Second*5)
				return nil
			}

			var msgID int
			var author int64
			buttons := Buttons{
				{
					caption: "Продолжить",
					handler: func(update *tgbotapi.Update, button *Button) bool {
						from := wd.GetUser(update)
						if from.ID != author {
							wd.AnswerCallbackQuery(update.CallbackQuery.ID, "Вопрос не для вас")
							return false
						}

						wd.DeleteMessage(chatID, msgID)

						randPlayer := wd.GetRandUser(chatID, msg.From.ID)
						if randPlayer == nil {
							wd.SendTTLMsg("Не смог получить оппонента", "", chatID, Buttons{}, time.Second*5)
							return true
						}

						players = append(players, randPlayer)

						wd.SendTTLMsg(fmt.Sprintf("Игра против %s.", randPlayer.Name), "", chatID, Buttons{}, time.Second*10)
						time.Sleep(time.Millisecond * 500)

						id := rand.Intn(len(players))
						player1, player2 := players[id], players[(id+1)%len(players)]

						if !shot(wd, msg.Chat, player1) {
							time.Sleep(time.Millisecond * 500)
							if !shot(wd, msg.Chat, player2) {
								wd.SendTTLMsg("Ура, никто не умер", "", chatID, Buttons{}, time.Second*5)
							}
						}

						return true
					},
				},
				{
					caption: "Отмена",
					handler: func(update *tgbotapi.Update, button *Button) bool {
						from := wd.GetUser(update)
						if from.ID != author {
							wd.AnswerCallbackQuery(update.CallbackQuery.ID, "Вопрос не для вас")
							return false
						}

						wd.DeleteMessage(chatID, msgID)
						return true
					},
				},
			}

			m, err := wd.SendMsg("Русская рулетка.\n"+
				"После нажатия на кнопку «Продолжить» случайным образом выбирается один участник чата. \n"+
				"Далее происходит два «выстрела»: один в тебя, другой — в выбранного участника. \n"+
				"Вероятность «попасть под пулю» составляет 1/6 \n"+
				"Если игрок «получает пулю», ему устанавливается режим только для чтения (RO) на 24 часа (администраторы имеют иммунитет). \n"+
				"Если после двух «выстрелов» никто не попадает под пулю, игра завершается, и никто не выбывает.", "", chatID, buttons)

			if err == nil {
				msgID = m.MessageID
				author = msg.From.ID
			} else {
				log.Println(errors.Wrap(err, "sendMsg error"))
			}
		case "russian_roulette_killed":
			killed, err := wd.r.Items(killedUsers)
			if err != nil {
				log.Println(errors.Wrap(err, "redis read error"))
				continue
			}

			viewKilled := make([]string, 0, len(killed))
			for _, data := range killed {
				tmp := new(KilledInfo)
				if err := json.Unmarshal([]byte(data), tmp); err == nil && tmp.UserName != "" {
					viewKilled = append(viewKilled, tmp.UserName)
				}
			}

			if len(viewKilled) > 0 {
				wd.SendTTLMsg(fmt.Sprintf("Убитые:\n%s", strings.Join(viewKilled, "\n")), "", chatID, Buttons{}, time.Second*10)
			} else {
				wd.SendTTLMsg("Убитых нет", "", chatID, Buttons{}, time.Second*10)
			}
		case "exampleconf":
			if f, err := os.CreateTemp("", "*.yaml"); err == nil {
				f.WriteString(confExample())
				f.Close()

				wd.SendFile(chatID, f.Name())
				os.RemoveAll(f.Name())
			}
			continue
		//case "test":
		// для теста
		//msg.NewChatMembers = &[]tgbotapi.User{
		//	{ID: 22, FirstName: "test", LastName: "test", UserName: "test", LanguageCode: "", IsBot: false},
		//}
		//continue
		case "clearLastMsg":
			wd.deleteLastMsg(msg.From.ID)
			continue
		case "allchats":
			fmt.Println(strings.Join(wd.getAllChats(), "\n"))
		case "help":
			msg := fmt.Sprintf("Антиспам для групп, при входе нового участника в группу бот задает вопрос, если ответа нет, участник блокируется.\n"+
				"Если нужно заблокировать пользователя тегните сообщения ботом (ответить на сообщение с текстом @%s).", me.UserName)
			wd.SendMsg(msg, "", chatID, Buttons{})
			continue
		case "checkAI":
			conf := readConf(wd, chatID)
			authKey := ""

			if conf != nil {
				authKey = conf.AI.GigaChat.AuthKey
			}

			split := strings.Split(msg.Text, "::")
			if len(split) >= 3 {
				authKey = strings.TrimSpace(split[2])
			} else if len(split) < 2 {
				wd.SendMsg("Не корректный формат сообщения", "", chatID, Buttons{})
				continue
			}

			if authKey == "" {
				wd.SendMsg("Не определен authKey для giga chat", "", chatID, Buttons{})
				continue
			}

			isSpam, percent, reason, err := wd.gigaClient(authKey).GetSpamPercent(split[1])
			if err != nil {
				wd.SendMsg(fmt.Sprintf("Произошла ошибка: %s", err.Error()), "", chatID, Buttons{})
			} else {
				wd.SendMsg(fmt.Sprintf("%v, %v, %s", isSpam, percent, reason), "", chatID, Buttons{})
			}
		default:
			if command != "" {
				fmt.Printf("Команда %s не поддерживается\n", command)
				continue
			} else {
				key := strconv.FormatInt(chatID, 10)
				if call, ok := wd.hooks[key]; ok {
					if call(update) {
						wd.DeleteMessage(chatID, msg.MessageID)

						delete(wd.hooks, key)
					}
				}
			}
		}

		if msg.LeftChatMember != nil && msg.LeftChatMember.ID == me.ID {
			wd.r.DeleteItems(strconv.FormatInt(wd.GetMessage(update).From.ID, 10), strconv.FormatInt(chatID, 10))
			continue
		}

		// вызов кворума на бан
		if strings.Contains(msg.Text, "@"+strings.TrimSpace(me.UserName)) && msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
			conf := readConf(wd, chatID)
			wd.StartVoting(msg, chatID, conf.CountVoted)
		}

		user := msg.From
		if txt := msg.Text; txt != "" {
			if s, r := wd.IsSPAM(user.ID, txt, readConf(wd, chatID)); s {
				time.Sleep(time.Millisecond * 500) // небольшая задержка, иногда сообщение в клиенте может отрисовываться после удаления
				wd.deleteSpam(user, r, msg.MessageID, chatID)
			}
		}
	}
}

func shot(wd *Telega, chat *tgbotapi.Chat, player *UserInfo) bool {
	result := lo.If(rand.Intn(6) == 1, "убит").Else("промах")
	wd.SendTTLMsg(fmt.Sprintf("Выстрел в игрока %s - %s.", player.Name, result), "", chat.ID, Buttons{}, time.Second*10)
	if result == "убит" {
		if wd.UserIsAdmin(chat.ChatConfig(), player.ID) {
			wd.SendTTLMsg(fmt.Sprintf("Игрок %s является администратором, у него иммунитет.", player.Name), "", chat.ID, Buttons{}, time.Second*10)
			return true
		}

		wd.r.AppendItems(killedUsers, (&KilledInfo{UserName: player.Name, To: time.Now().Add(time.Hour * 24)}).String())
		wd.DisableSendMessages(chat.ID, player.ID, time.Hour*24)
		return true
	}

	return false
}

//func processingNewMembers(wd *Telega, update tgbotapi.Update) bool {
//	var newChatMember []*tgbotapi.User
//	var chat tgbotapi.Chat
//	var messageID int
//	var from *tgbotapi.User
//
//	if update.ChatMember != nil && update.ChatMember.NewChatMember.Status == "member" {
//		newChatMember = append(newChatMember, update.ChatMember.NewChatMember.User)
//		chat, from = update.ChatMember.Chat, &update.ChatMember.From
//	}
//
//	if msg := wd.GetMessage(update); msg != nil {
//		users := lo.Map(msg.NewChatMembers, func(item tgbotapi.User, _ int) *tgbotapi.User {
//			return &item
//		})
//
//		newChatMember = lo.UniqBy(append(newChatMember, users...), func(item *tgbotapi.User) int64 { return item.ID })
//		chat, messageID, from = *msg.Chat, msg.MessageID, msg.From
//	}
//
//	for _, user := range newChatMember {
//		if handlerAddNewMembers(wd, chat, user, from, readConf(wd, chat.ID)) {
//			wd.DeleteMessage(chat.ID, messageID)
//		}
//	}
//
//	return len(newChatMember) > 0
//}

func configuration(wd *Telega, update tgbotapi.Update, chatID int64) {
	buttons := Buttons{}
	chats, _ := wd.r.Items(strconv.FormatInt(wd.GetMessage(update).From.ID, 10))
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

				wd.SendTTLMsg("👍", "", chatID, Buttons{}, time.Second*10)
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

func handlerAddNewMembers(wd *Telega, chat tgbotapi.Chat, appendedUser *tgbotapi.User, from *tgbotapi.User, conf *Conf) {
	var questionMessageID int

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
						wd.DeleteMessage(chat.ID, message.MessageID)
						break
					}
				}
			}()
		}

		strChatID := strconv.FormatInt(chat.ID, 10)
		key := strconv.FormatInt(from.ID, 10)
		wd.r.AppendItems(key, strChatID)
		wd.r.Set(strChatID, chat.Title, -1)

		log.Printf("бота добавили в чат, chatID: %s (добавил %s)\n", strChatID, key)

		return
	}

	if conf == nil {
		log.Printf("для чата %s %s (%s) не определены настройки\n", chat.FirstName, chat.LastName, chat.UserName)
		return
	}

	if wd.CheckAndBlockMember(chat.ID, appendedUser, conf) {
		return // значит уже заблокировали
	}

	log.Println(fmt.Sprintf("join new user: %s %s (%s), chat: %s", appendedUser.FirstName, appendedUser.LastName, appendedUser.UserName, chat.UserName))

	wd.DisableSendMessages(chat.ID, appendedUser.ID, 0) // ограничиваем пользователя писать сообщения пока он не ответит верно на вопрос

	ctx, cancel := context.WithCancel(context.Background())

	var handlers []func(*tgbotapi.Update, *Button) bool
	deleteQuestionMessage := func(messageID int) {
		cancel()
		if err := wd.DeleteMessage(chat.ID, messageID); err == nil {
			wd.r.DeleteItems(keyActiveMSG, strconv.Itoa(messageID))
		}
	}
	handlerCancel := func(update *tgbotapi.Update, _ *Button) (result bool) {
		from := wd.GetUser(update)
		if result = update == nil || from.ID == appendedUser.ID || wd.UserIsAdmin(chat.ChatConfig(), from.ID); result {
			deleteQuestionMessage(questionMessageID)
			wd.KickChatMember(chat.ID, *appendedUser)
		}
		return result
	}

	b := Buttons{}
	for _, ans := range conf.Answers {
		a := ans // для замыкания
		handlers = append(handlers, func(update *tgbotapi.Update, currentButton *Button) (result bool) {
			from := wd.GetUser(update)
			if result = from.ID == appendedUser.ID || wd.UserIsAdmin(chat.ChatConfig(), from.ID); result {
				if a.Correct {
					wd.EnableWritingMessages(chat.ID, appendedUser.ID)
					deleteQuestionMessage(questionMessageID)
				} else {
					deleteQuestionMessage(questionMessageID)
					wd.KickChatMember(chat.ID, *appendedUser)
				}
			} else {
				add := "к тому же вы ответили не верно."
				if a.Correct {
					add = "но вы ответили верно, молодцом!"
				}
				wd.AnswerCallbackQuery(update.CallbackQuery.ID, "Вопрос не для вас, "+add)
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
		handler: handlerCancel,
		timer:   timeout,
	})

	member := appendedUser.FirstName + " " + appendedUser.LastName
	if appendedUser.UserName != "" {
		member = "@" + appendedUser.UserName
	}

	txt := fmt.Sprintf("Привет %s\nДля проверки на антиспам просьба ответить на вопрос (на ответ дается %d секунд):"+
		"\n\n%s", member, timeout, conf.Question.Txt)

	if message, err := wd.SendMsg(txt, conf.Question.Img, chat.ID, b); err != nil {
		log.Println(errors.Wrap(err, "SendMsg error"))
		return
	} else {
		questionMessageID = message.MessageID
	}

	wd.r.AppendItems(keyActiveMSG, strconv.Itoa(questionMessageID))

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second * time.Duration(timeout)):
			log.Println("timeout has ended")
			deleteQuestionMessage(questionMessageID)
			wd.KickChatMember(chat.ID, *appendedUser)
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
