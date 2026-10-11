package app

import (
	"context"
	"fmt"
	"github.com/alecthomas/kingpin/v2"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"golang.org/x/time/rate"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type wrapper struct {
	settings map[string]string
	err      error
}

var (
	botToken    string
	webhookURL  string
	port        string
	redisAddr   string
	cert        string
	pollingMode string
	ownerID     string
	proxyURL    string
)

var (
	kp    *kingpin.Application
	debug bool
)

const (
	settingsKey = "questions"
	lastMsgKey  = "lastMsg"
	userInfo    = "userInfo"
	killedUsers = "killedUsers"
)

func init() {
	kp = kingpin.New("Антиспам бот", "")
	kp.Flag("debug", "вывод отладочной информации").Short('d').BoolVar(&debug)

	_ = godotenv.Load()
	botToken = os.Getenv("BotToken")
	webhookURL = os.Getenv("WebhookURL")
	port = os.Getenv("PORT")
	redisAddr = os.Getenv("REDIS")
	cert = os.Getenv("CRT")
	pollingMode = os.Getenv("POLLING_MODE")
	ownerID = os.Getenv("OWNER_ID")   // используется для некоторых функций бота
	proxyURL = os.Getenv("PROXY_URL") // прокси для запросов к Telegram API (http/https/socks5), например socks5://user:pass@host:port
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

	InitDefaultLogger()

	logger := slog.Default()

	wd := new(Telega)
	wdUpdate, err := wd.New(debug, cert, pollingMode == "1")
	if err != nil {
		logger.Error(errors.Wrap(err, "create telegrtam client error").Error())
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(ctx_)
	go shutdown(wd, cancel)

	limiter := rate.Sometimes{Interval: time.Second * 30} // лимит на вызов апи гигачата

	for {
		var update models.Update

		select {
		case <-ctx.Done():
			return nil
		case update = <-wdUpdate:
		}

		chatMember := lo.If(update.ChatMember != nil, update.ChatMember).Else(update.MyChatMember)
		if chatMember != nil &&
			chatMember.NewChatMember.Type == models.ChatMemberTypeMember &&
			chatMember.OldChatMember.Type != models.ChatMemberTypeRestricted &&
			chatMember.OldChatMember.Type != models.ChatMemberTypeAdministrator {
			handlerAddNewMembers(wd, chatMember.Chat, chatMemberUser(chatMember.NewChatMember), &chatMember.From, readConf(wd, chatMember.Chat.ID))
		}

		msg := wd.GetMessage(update)
		if msg == nil {
			continue
		}

		chatID := msg.Chat.ID
		wd.SaveMember(chatID, wd.GetUser(&update))

		// удаляем сообщения о вступлении в группу
		if len(msg.NewChatMembers) > 0 {
			wd.DeleteMessage(chatID, msg.ID)
			continue
		}

		me, _ := wd.bot.GetMe(ctx)
		if p := strings.Split(msg.Text, "@"); msg.Text != "" && msg.Text[0] == '/' && len(p) == 2 && p[1] != me.Username {
			continue
		}

		// обработка команд кнопок
		if wd.CallbackQuery(update) {
			continue
		}

		command := messageCommand(msg)
		switch command {
		case "start":
			wd.start(chatID, msg)
			continue
		case "configuration":
			wd.configuration(chatID, update)
			continue
		case "random_moderator":
			go wd.randomModeratorAutoExtend(chatID, msg)
			continue
		case "russian_roulette":
			wd.russianRoulette(chatID, msg)
			continue
		case "russian_roulette_killed":
			wd.russianRouletteKilled(chatID)
			continue
		case "exampleconf":
			wd.exampleConf(chatID)
			continue
		case "clearLastMsg":
			wd.deleteLastMsg(msg.From.ID)
			continue
		case "allchats":
			wd.allChats(msg)
			continue
		case "help":
			wd.help(chatID)
			continue
		case "checkAI":
			wd.checkAI(chatID, msg)
			continue
		case "notify":
			wd.notify(chatID, msg)
			continue
		default:
			if command != "" {
				logger.Info(fmt.Sprintf("Команда %s не поддерживается\n", command))
				continue
			} else {
				key := strconv.FormatInt(chatID, 10)
				if call, ok := wd.hooks[key]; ok {
					if call(update) {
						wd.DeleteMessage(chatID, msg.ID)

						delete(wd.hooks, key)
					}
				}
				wd.storeMessage(update.Message)
			}
		}

		if msg.LeftChatMember != nil && msg.LeftChatMember.ID == me.ID {
			_ = wd.r.DeleteItems(strconv.FormatInt(wd.GetMessage(update).From.ID, 10), strconv.FormatInt(chatID, 10))
			continue
		}

		// вызов кворума на бан
		if strings.Contains(msg.Text, "@"+strings.TrimSpace(me.Username)) && msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
			conf := readConf(wd, chatID)
			wd.StartVoting(msg, chatID, conf.CountVoted)
		}

		user := msg.From
		if txt := msg.Text; txt != "" {
			if s, r := wd.IsSPAM(user.ID, chatID, txt, readConf(wd, chatID)); s {
				time.Sleep(time.Millisecond * 500) // небольшая задержка, иногда сообщение в клиенте может отрисовываться после удаления
				wd.deleteSpam(user, r, msg.ID, chatID)
			} else {
				limiter.Do(func() {
					wd.CheckMessageModeration(msg, readConf(wd, chatID))
				})
			}
		}
	}
}

func shot(wd *Telega, chat *models.Chat, player *UserInfo) bool {
	result := lo.If(rand.Intn(6) == 1, "убит").Else("промах")
	wd.SendTTLMsg(fmt.Sprintf("Выстрел в игрока %s - %s.", player.Name, result), chat.ID, Buttons{}, time.Second*10)
	if result == "убит" {
		if wd.UserIsAdmin(chat.ID, player.ID) {
			wd.SendTTLMsg(fmt.Sprintf("Игрок %s является администратором, у него иммунитет.", player.Name), chat.ID, Buttons{}, time.Second*10)
			return true
		}

		wd.r.AppendItems(killedUsers, (&KilledInfo{UserName: player.Name, To: time.Now().Add(time.Hour * 24)}).String())
		wd.DisableSendMessages(chat.ID, player.ID, time.Hour*24)
		return true
	}

	return false
}

func configuration(wd *Telega, update models.Update, chatID int64) {
	buttons := Buttons{}
	chats, _ := wd.r.Items(strconv.FormatInt(wd.GetMessage(update).From.ID, 10))
	key := strconv.FormatInt(chatID, 10)

	var msg *models.Message
	handler := func(_ *models.Update, b *Button) bool {
		chat := b.ID
		buttons := Buttons{
			&Button{
				caption: "Скачать",
				handler: func(*models.Update, *Button) bool {
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

		wd.hooks[key] = func(upd models.Update) bool {
			if upd.Message == nil {
				return false
			}
			if confdata, err := wd.ReadFile(upd.Message); err == nil {
				settings, err := wrap(wd.r.StringMap(settingsKey)).result()
				if err != nil {
					wd.SendMsg(wd.ctx, fmt.Sprintf("Произошла ошибка при получении значения из redis:\n%v", err), chatID, 0, Buttons{})
				}

				settings[chat] = confdata
				wd.r.SetMap(settingsKey, settings)

				wd.SendTTLMsg("👍", chatID, Buttons{}, time.Second*10)
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

	msg, _ = wd.SendMsg(wd.ctx, "выберите чат", chatID, 0, buttons)
}

func getSettings(wd *Telega, key string, chatID int64) bool {
	logger := wd.logger.With("chatID", chatID)

	settings, err := wrap(wd.r.StringMap(settingsKey)).result()
	if err != nil {
		logger.Error(fmt.Errorf("ошибка получения конфига из redis: %w", err).Error())
		return false
	}

	if s, ok := settings[key]; ok {
		if f, err := os.CreateTemp("", "*.yaml"); err == nil {
			defer os.RemoveAll(f.Name())

			f.WriteString(s)
			f.Close()

			if err := wd.SendFile(wd.ctx, chatID, f.Name()); err != nil {
				return false
			}
		}
	} else {
		logger.Warn(fmt.Sprintf("в настройках отсутствует ключ %s", key))
		return false
	}

	return true
}

func handlerAddNewMembers(wd *Telega, chat models.Chat, appendedUser *models.User, from *models.User, conf *Conf) {
	var questionMessageID int

	// когда добавили бота в чат, проверяем является ли он админом, если нет, сообщаем что нужно добавить в группу
	me, _ := wd.bot.GetMe(wd.ctx)
	if appendedUser.ID == me.ID {
		if !wd.MeIsAdmin(chat.ID) {
			message, err := wd.SendMsg(wd.ctx, "Для корректной работы сделайте меня администратором", chat.ID, 0, Buttons{})
			if err != nil {
				wd.logger.Error(errors.Wrap(err, "send msg error").Error())
			}

			// отслеживаем статус, когда сделают администратором удаляем сообщение
			go func() {
				tick := time.NewTicker(time.Second)
				defer tick.Stop()

				for range tick.C {
					if wd.MeIsAdmin(chat.ID) {
						wd.DeleteMessage(chat.ID, message.ID)
						break
					}
				}
			}()
		}

		strChatID := strconv.FormatInt(chat.ID, 10)
		key := strconv.FormatInt(from.ID, 10)
		wd.r.AppendItems(key, strChatID)
		wd.r.Set(strChatID, chat.Title, -1)

		wd.logger.Info(fmt.Sprintf("бота добавили в чат, chatID: %s (добавил %s)", strChatID, key))

		return
	}

	if conf == nil {
		wd.logger.Error(fmt.Sprintf("для чата %s %s (%s) не определены настройки", chat.FirstName, chat.LastName, chat.Username))
		return
	}

	if wd.CheckAndBlockMember(chat.ID, appendedUser, conf) {
		return // значит уже заблокировали
	}

	wd.logger.With("chatID", chat.ID).Info(fmt.Sprintf("join new user: %s (%d) to chat: %s", wd.UserString(appendedUser), appendedUser.ID, chat.Username))

	wd.DisableSendMessages(chat.ID, appendedUser.ID, 0) // ограничиваем пользователя писать сообщения пока он не ответит верно на вопрос

	ctx, cancel := context.WithCancel(context.Background())

	var handlers []func(*models.Update, *Button) bool
	deleteQuestionMessage := func(messageID int) {
		cancel()
		if err := wd.DeleteMessage(chat.ID, messageID); err == nil {
			wd.r.DeleteItems(keyActiveMSG, strconv.Itoa(messageID))
		}
	}
	handlerCancel := func(update *models.Update, _ *Button) (result bool) {
		from := wd.GetUser(update)
		if result = update == nil || from.ID == appendedUser.ID || wd.UserIsAdmin(chat.ID, from.ID); result {
			deleteQuestionMessage(questionMessageID)
			wd.KickChatMember(chat.ID, *appendedUser)
		}
		return result
	}

	b := Buttons{}
	for _, ans := range conf.Answers {
		a := ans // для замыкания
		handlers = append(handlers, func(update *models.Update, currentButton *Button) (result bool) {
			from := wd.GetUser(update)
			if result = from.ID == appendedUser.ID || wd.UserIsAdmin(chat.ID, from.ID); result {
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
	if appendedUser.Username != "" {
		member = "@" + appendedUser.Username
	}

	txt := fmt.Sprintf("Привет %s\nДля проверки на антиспам просьба ответить на вопрос (на ответ дается %d секунд):"+
		"\n\n%s", member, timeout, conf.Question.Txt)

	var message *models.Message
	var err error
	if conf.Question.Img != "" {
		message, err = wd.sendPhoto(wd.ctx, txt, conf.Question.Img, chat.ID, appendedUser.ID, 0, b)
	} else {
		message, err = wd.SendMsg(wd.ctx, txt, chat.ID, appendedUser.ID, b)
	}
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "SendMsg error").Error())
		return
	}
	questionMessageID = message.ID

	wd.r.AppendItems(keyActiveMSG, strconv.Itoa(questionMessageID))

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second * time.Duration(timeout)):
			wd.logger.With("chatID", chat.ID).Info(fmt.Sprintf("the user %d did not answer the question during the timeout\n", appendedUser.ID))
			deleteQuestionMessage(questionMessageID)
			wd.KickChatMember(chat.ID, *appendedUser)
		}
	}()
}

func readConf(wd *Telega, chatID int64) *Conf {
	settings, err := wrap(wd.r.StringMap(settingsKey)).result() // todo переделать на мапу в памяти как lastMsg
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "read settings error").Error())
		return nil
	}

	key := strconv.FormatInt(chatID, 10)
	confStr, ok := settings[key]
	if !ok {
		wd.logger.Info(fmt.Sprintf("settings not found for chat: %d", chatID))
		return nil
	}

	conf, err := LoadConf([]byte(confStr))
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "LoadConf error").Error(), "chatID", chatID)
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

	wd.logger.Info("shutting down")
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
