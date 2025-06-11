package app

import (
	"Antispam/db"
	"Antispam/giga"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/k0kubun/pp/v3"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"io"
	"io/ioutil"
	"log"
	"log/slog"
	"maps"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	uuid "github.com/nu7hatch/gouuid"
)

type UserInfo struct {
	ID     int64
	Name   string
	Weight int64
}

type Button struct {
	caption string
	handler func(*tgbotapi.Update, *Button) bool
	timer   int

	// время начала таймера
	start time.Time
	ID    string
}

const (
	keyActiveMSG             = "keyActiveMSG"
	keyActiveRandomModerator = "keyActiveRandomModerator"
	promoteChatMember        = "promoteChatMember"
	keyUsingOneTry           = "UsingOneTry"
)

type Buttons []*Button

//go:generate mockgen -source=$GOFILE -destination=./mock/mock.go
type IRedis interface {
	StringMap(key string) (map[string]string, error)
	Keys() []string
	KeysMask(mask string) []string
	Get(key string) (string, error)
	Delete(key string) error
	SetMap(key string, value map[string]string)
	Set(key, value string, ttl time.Duration) error
	Items(key string) ([]string, error)
	DeleteItems(key, value string) error
	AppendItems(key, value string)
	KeyExists(key string) bool
}

type Telega struct {
	bot               *tgbotapi.BotAPI
	callback          map[string]func(tgbotapi.Update) bool
	hooks             map[string]func(tgbotapi.Update) bool
	running           int32
	r                 IRedis
	pool              sync.Map
	lastMsg           map[string]string // для хранения последнего сообщения по пользователю
	mx                sync.RWMutex
	httpServer        *http.Server
	users             map[int64]map[int64]UserInfo
	ctx               context.Context
	cancel            context.CancelFunc
	randomModeratorMX sync.Mutex
	logger            *slog.Logger
}

type KilledInfo struct {
	UserID   int64
	UserName string
	To       time.Time
}

func (wd *Telega) New(debug bool, certFilePath string, pollingMode bool) (result tgbotapi.UpdatesChannel, err error) {
	wd.callback = map[string]func(tgbotapi.Update) bool{}
	wd.hooks = map[string]func(tgbotapi.Update) bool{}
	wd.users = map[int64]map[int64]UserInfo{}
	wd.logger = slog.Default().With("name", "telegram")

	wd.r, err = new(db.Redis).Create(redisAddr)
	if err != nil {
		return nil, errors.Wrap(err, "create redis")
	}

	// восстанавливаем данные из БД
	wd.mx.Lock()
	wd.lastMsg, _ = wd.r.StringMap(lastMsgKey)
	wd.mx.Unlock()

	wd.ctx, wd.cancel = context.WithCancel(context.Background())

	wd.restoreUsersInfo()
	go wd.watchKilledUsers(time.Minute)

	wd.bot, err = tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, err
	}
	if webhookURL == "" && !pollingMode {
		// для отладки получаем через ngrok
		if webhookURL = getngrokWebhookURL(); webhookURL == "" {
			return nil, errors.New("не удалось получить WebhookURL")
		}
	}

	wd.bot.Debug = debug
	wd.httpServer = &http.Server{Addr: ":" + port}
	go wd.httpServer.ListenAndServe()

	go wd.watchActiveRandomModerator(time.Minute)

	wd.logger.Info(fmt.Sprintf("listen port: %s, debug: %v", port, debug))

	if !pollingMode {
		var wh tgbotapi.WebhookConfig
		if certFilePath != "" {
			f, err := os.Open(certFilePath)
			if err != nil {
				return nil, errors.Wrap(err, "open cert error")
			}

			b, _ := io.ReadAll(f)
			fileBytes := tgbotapi.FileBytes{Bytes: b}

			wh, err = tgbotapi.NewWebhookWithCert(webhookURL, fileBytes)
			if err != nil {
				return nil, errors.Wrap(err, "get webhook error")
			}
		} else {
			if wh, err = tgbotapi.NewWebhook(webhookURL); err != nil {
				return nil, errors.Wrap(err, "get webhook error")
			}
		}

		wh.MaxConnections = 70
		wh.AllowedUpdates = []string{"message", "chat_member", "callback_query", "chat_join_request", "my_chat_member"}
		_, err = wd.bot.Request(wh)
		if err != nil {
			return nil, errors.Wrap(err, "request error")
		}

		return wd.bot.ListenForWebhook("/"), nil // вебхук
	} else {
		_, err = wd.bot.Request(&tgbotapi.DeleteWebhookConfig{}) // при использовании полинга
		u := tgbotapi.NewUpdate(0)
		u.Timeout = 60
		u.AllowedUpdates = []string{"message", "chat_member", "callback_query", "chat_join_request", "my_chat_member"}
		return wd.bot.GetUpdatesChan(u), nil // полинг
	}
}

func (wd *Telega) SendTTLMsg(msg string, imgURL string, chatID int64, buttons Buttons, ttl time.Duration) (*tgbotapi.Message, error) {
	if m, err := wd.SendMsg(msg, imgURL, chatID, buttons); err != nil {
		return nil, err
	} else {
		go func() {
			time.Sleep(ttl)
			wd.DeleteMessage(chatID, m.MessageID)
		}()

		return m, nil
	}
}

func (wd *Telega) SendMsg(msg string, imgURL string, chatID int64, buttons Buttons) (*tgbotapi.Message, error) {
	if imgURL != "" {
		if path, err := downloadFile(imgURL); err == nil {
			defer os.RemoveAll(path)

			newmsg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(path))
			newmsg.Caption = msg
			newmsg.ParseMode = "HTML"
			return wd.createButtonsAndSend(&newmsg, buttons)
		} else {
			return &tgbotapi.Message{}, err
		}
	} else {
		newmsg := tgbotapi.NewMessage(chatID, msg)
		newmsg.ParseMode = "HTML"
		return wd.createButtonsAndSend(&newmsg, buttons)
	}
}

func (wd *Telega) getAllChats() []string {
	var result []string

	for _, key := range wd.r.Keys() {
		if title, err := wd.r.Get(key); err == nil {
			result = append(result, key+"::"+title)
		}
	}

	return result
}

func (wd *Telega) ReplyMsg(msg string, imgURL string, chatID int64, buttons Buttons, parrentMessageID int) (*tgbotapi.Message, error) {
	if imgURL != "" {
		if path, err := downloadFile(imgURL); err == nil {
			defer os.RemoveAll(path)

			newmsg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(path))
			newmsg.ReplyToMessageID = parrentMessageID
			newmsg.Caption = msg
			newmsg.ParseMode = "HTML"
			return wd.createButtonsAndSend(&newmsg, buttons)
		} else {
			return &tgbotapi.Message{}, err
		}
	} else {
		newmsg := tgbotapi.NewMessage(chatID, msg)
		newmsg.ReplyToMessageID = parrentMessageID
		newmsg.ParseMode = "HTML"
		return wd.createButtonsAndSend(&newmsg, buttons)
	}
}

func (wd *Telega) SendFile(chatID int64, filepath string) error {
	msg := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filepath))
	_, err := wd.bot.Send(msg)
	return err
}

func (wd *Telega) createButtonsAndSend(msg tgbotapi.Chattable, buttons Buttons) (*tgbotapi.Message, error) {
	// if _, ok := msg.(tgbotapi.Fileable); ok {
	//	fmt.Println(1)
	// }

	if len(buttons) > 0 {
		buttons.createButtons(msg, wd.callback, func() {}, 2)
	}

	//timerExist := false
	//for _, b := range buttons {
	//	if timerExist = b.timer > 0; timerExist {
	//		break
	//	}
	//}

	m, err := wd.bot.Send(msg)

	// Отключен таймер на кнопке т.к. при большом количествет присоединившихся пользователях не будет работать
	// if timerExist {
	// 	go t.setTimer(m, buttons, cxt, cancel) // таймер кнопки
	// }

	return &m, err
}

func (wd *Telega) EditMsg(msg *tgbotapi.Message, txt string, buttons Buttons) *tgbotapi.Message {
	editmsg := tgbotapi.NewEditMessageText(msg.Chat.ID, msg.MessageID, txt)
	editmsg.ParseMode = "HTML"
	m, _ := wd.createButtonsAndSend(&editmsg, buttons)

	return m
}

func (wd *Telega) MeIsAdmin(chatConfig tgbotapi.ChatConfig) bool {
	me, _ := wd.bot.GetMe()
	return wd.UserIsAdmin(chatConfig, me.ID)
}

func (wd *Telega) getChatAdministrators(chatConfig tgbotapi.ChatConfig) []tgbotapi.ChatMember {
	admins, _ := wd.bot.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{ChatConfig: chatConfig})
	return admins
}

func (wd *Telega) UserIsAdmin(chatConfig tgbotapi.ChatConfig, userID int64) bool {
	for _, a := range wd.getChatAdministrators(chatConfig) {
		if a.User.ID == userID {
			return true
		}
	}

	return false
}

func (wd *Telega) AppointModerator(chatID int64, user *UserInfo, deadline time.Time) error {
	params := map[string]string{
		"chat_id":              strconv.FormatInt(chatID, 10),
		"user_id":              strconv.FormatInt(user.ID, 10),
		"can_change_info":      "false",
		"can_delete_messages":  "true",
		"can_invite_users":     "true",
		"can_restrict_members": "true",
		"can_pin_messages":     "false",
		"can_promote_members":  "false",
	}

	_, err := wd.bot.MakeRequest(promoteChatMember, params)
	if err == nil {
		wd.storeNewModerator(chatID, user, deadline)
	}

	return err
}

func (wd *Telega) RemoveModerator(chatID, userID int64) error {
	params := map[string]string{
		"chat_id":              strconv.FormatInt(chatID, 10),
		"user_id":              strconv.FormatInt(userID, 10),
		"can_change_info":      "false",
		"can_delete_messages":  "false",
		"can_invite_users":     "false",
		"can_restrict_members": "false",
		"can_pin_messages":     "false",
		"can_promote_members":  "false",
	}

	_, err := wd.bot.MakeRequest(promoteChatMember, params)
	if err == nil {
		wd.r.Delete(keyActiveRandomModerator + params["chat_id"])
	}

	return err
}

func (wd *Telega) storeNewModerator(chatID int64, user *UserInfo, deadline time.Time) {
	wd.mx.Lock()
	defer wd.mx.Unlock()

	data := map[string]interface{}{
		"ChatID":   chatID,
		"User":     user,
		"Deadline": deadline.Format(time.RFC1123),
	}

	d, err := json.Marshal(&data)
	if err != nil {
		return
	}

	wd.r.Set(keyActiveRandomModerator+strconv.FormatInt(chatID, 10), string(d), -1)
}

func (wd *Telega) GetActiveRandModerator(chatID int64) (string, time.Time) {
	wd.mx.RLock()
	defer wd.mx.RUnlock()

	defer func() {
		if e := recover(); e != nil {
			wd.logger.Error(fmt.Sprintf("PANIC: %v", e))
		}
	}()

	v, err := wd.r.Get(keyActiveRandomModerator + strconv.FormatInt(chatID, 10))
	if err != nil {
		return "", time.Time{}
	}

	data := map[string]interface{}{}
	if err := json.Unmarshal([]byte(v), &data); err != nil {
		return "", time.Time{}
	}

	deadline, _ := time.Parse(time.RFC1123, data["Deadline"].(string))
	return data["User"].(map[string]interface{})["Name"].(string), deadline
}

func (wd *Telega) UserIsCreator(chatConfig tgbotapi.ChatConfig, userID int64) bool {
	for _, a := range wd.getChatAdministrators(chatConfig) {
		if a.IsCreator() && a.User.ID == userID {
			return true
		}
	}

	return false
}

func (wd *Telega) setTimer(msg tgbotapi.Message, buttons Buttons, cxt context.Context, cancel context.CancelFunc) {
	tick := time.NewTicker(wd.getDelay())
	defer func() {
		tick.Stop()
	}()

	for i := 0; i < len(buttons); i++ {
		if buttons[i].timer > 0 {
			buttons[i].start = time.Now().Add(time.Second * time.Duration(buttons[i].timer))
		}
	}

B:
	for {
		select {
		case <-cxt.Done():
			break B
		default:
			var button *Button
			for i := 0; i < len(buttons); i++ {
				if !buttons[i].start.IsZero() && buttons[i].start.Before(time.Now()) {
					button = buttons[i]
				}
			}

			wd.EditButtons(&msg, buttons)

			if button != nil {
				if button.handler != nil {
					button.handler(nil, button)
				}

				delete(wd.callback, button.ID)
				break B
			}

			<-tick.C
		}
	}
}

func (wd *Telega) CallbackQuery(update tgbotapi.Update) bool {
	if update.CallbackQuery == nil || update.CallbackQuery.Message == nil {
		return false
	}

	if call, ok := wd.callback[update.CallbackQuery.Data]; ok {
		if call != nil {
			if call(update) {
				wd.DeleteMessage(update.CallbackQuery.Message.Chat.ID, update.CallbackQuery.Message.MessageID)
				delete(wd.callback, update.CallbackQuery.Data)
			}
		}
	}

	return true
}

func (wd *Telega) downloadFile(filepath, url string) error {
	resp, err := new(http.Client).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func (wd *Telega) GetUser(update *tgbotapi.Update) *tgbotapi.User {
	if update == nil {
		return nil
	} else if update.Message != nil {
		return update.Message.From
	} else if update.CallbackQuery != nil {
		return update.CallbackQuery.From
	} else {
		return nil
	}
}

func (wd *Telega) GetMessage(update tgbotapi.Update) *tgbotapi.Message {
	if update.Message != nil {
		return update.Message
	} else if update.CallbackQuery != nil {
		return update.CallbackQuery.Message
	} else {
		return nil
	}
}

func (wd *Telega) ReadFile(message *tgbotapi.Message) (data string, err error) {
	// message.Chat.ID
	downloadFilebyID := func(FileID string) {
		var file tgbotapi.File
		if file, err = wd.bot.GetFile(tgbotapi.FileConfig{FileID}); err == nil {
			_, fileName := path.Split(file.FilePath)
			filePath := path.Join(os.TempDir(), fileName)
			defer os.Remove(filePath)

			err = wd.downloadFile(filePath, file.Link(botToken))
			if err == nil {
				if dataByte, err := ioutil.ReadFile(filePath); err == nil {
					data = string(dataByte)
				}
			}
		}
	}

	if message.Document != nil {
		downloadFilebyID(message.Document.FileID)
	} else {
		return "", fmt.Errorf("Не поддерживаемый тип данных")
	}

	return data, err
}

func (wd *Telega) DisableSendMessages(chatID int64, userID int64, duration time.Duration) {
	wd.restrictChatMemberConfig(chatID, userID, duration, false)
}

func (wd *Telega) EnableWritingMessages(chatID int64, userID int64) {
	wd.restrictChatMemberConfig(chatID, userID, 0, true)
}

func (wd *Telega) restrictChatMemberConfig(chatID int64, userID int64, duration time.Duration, allow bool) {
	var untilDate int64
	if duration > 0 {
		untilDate = time.Now().Add(duration).Unix()
	}

	conf := tgbotapi.RestrictChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: chatID,
			UserID: userID,
		},
		UntilDate: untilDate,
		Permissions: &tgbotapi.ChatPermissions{
			CanSendMessages:       allow,
			CanSendMediaMessages:  allow,
			CanSendPolls:          allow,
			CanSendOtherMessages:  allow,
			CanAddWebPagePreviews: allow,
			CanChangeInfo:         allow,
			CanInviteUsers:        allow,
			CanPinMessages:        allow,
		},
	}

	//mem, err := t.bot.GetChatMember(tgbotapi.GetChatMemberConfig{
	//	ChatConfigWithUser: tgbotapi.ChatConfigWithUser{UserID: userID, ChatID: chatID},
	//})

	_, err := wd.bot.Request(conf)
	if err != nil {
		wd.logger.Error(fmt.Sprintf("При изменении прав пользователя произошла ошибка: %v\n", err))
	}
}

func (wd *Telega) KickChatMember(chatID int64, user tgbotapi.User) {
	go func() {
		userName := wd.UserString(&user)
		users, err := wd.r.Items(keyUsingOneTry)
		if wd.secondAttempt(users, err, strconv.FormatInt(user.ID, 10)) {
			wd.kickChatMember(chatID, user.ID)
			wd.r.DeleteItems(keyUsingOneTry, strconv.FormatInt(user.ID, 10))
			return
		}

		_ = userName
		// todo временно
		//t.r.AppendItems(key, strconv.FormatInt(user.ID, 10))
		//msg, _ := t.SendMsg(fmt.Sprintf("%s, мне придется Вас удалить из чата, но у Вас будет еще одна попытка входа.", userName), "", chatID, Buttons{})
		//time.Sleep(time.Second * 10)
		//t.kickChatMember(chatID, user.ID)
		//t.unbanChatMember(chatID, user.ID)
		//t.DeleteMessage(chatID, msg.MessageID)
	}()
}

func (wd *Telega) secondAttempt(users []string, err error, userID string) bool {
	if err != nil {
		return false
	}
	for _, item := range users {
		if userID == item {
			return true
		}
	}

	return false
}

func (wd *Telega) deleteLastMsg(userID int64) {
	wd.mx.Lock()
	defer wd.mx.Unlock()

	delete(wd.lastMsg, strconv.FormatInt(userID, 10))
}

func (wd *Telega) deleteAllLastMsg() {
	wd.mx.Lock()
	defer wd.mx.Unlock()

	wd.lastMsg = map[string]string{}
}

// CheckMessage проверяет сообщение на токсичность и оффтоп
func (wd *Telega) CheckMessage(msg *tgbotapi.Message, conf *Conf) {
	if msg == nil || conf == nil || conf.AI.GigaChat.AuthKey == "" {
		return
	}

	if userWeight := wd.userWeight(msg.Chat.ID, msg.From.ID); userWeight > 5 {
		wd.logger.Debug(fmt.Sprintf("user: %s in chat: %s skipped, userWeight: %d", msg.From.String(), msg.Chat.Title, userWeight))
		return
	}

	c := wd.gigaClient(msg.Chat.ID, conf.AI.GigaChat.AuthKey)
	analysis, err := c.GetMessageCharacteristics(strings.ReplaceAll(msg.Text, "\n", " "))
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "GetMessageCharacteristics error").Error())
		return
	}

	if analysis.IsToxic {
		wd.ReplyMsg(fmt.Sprintf("Ваше сообщение похоже на токсичное, вот почему:  %s", analysis.ToxicReason), "", msg.Chat.ID, Buttons{}, msg.MessageID)
	}

	if analysis.IsOffTopic {
		wd.ReplyMsg("Ваше сообщение не относится к тематике чата", "", msg.Chat.ID, Buttons{}, msg.MessageID)
	}
}

func (wd *Telega) IsSPAM(userID, chatID int64, msg string, conf *Conf) (bool, string) {
	if conf == nil || conf.AI.GigaChat.AuthKey == "" {
		return false, ""
	}

	wd.mx.Lock()
	defer wd.mx.Unlock()

	logger := wd.logger.With("userID", userID, "chatID", chatID)

	// если есть последнее сообщение тогда выходим, не проверяем
	if _, ok := wd.lastMsg[strconv.FormatInt(userID, 10)]; ok {
		return false, ""
	}

	c := wd.gigaClient(chatID, conf.AI.GigaChat.AuthKey)
	analysis, err := c.GetMessageCharacteristics(strings.ReplaceAll(msg, "\n", " "))
	if err != nil {
		logger.Error(errors.Wrap(err, "getSpamPercent error").Error())
		return false, ""
	}

	p := pp.New()
	p.SetColoringEnabled(false)

	logger.Info(fmt.Sprintf("msg: %s, IsSPAM: %v", msg, p.Sprint(analysis)))
	if !analysis.IsSpam {
		wd.lastMsg[strconv.FormatInt(userID, 10)] = msg
	}

	return analysis.IsSpam, analysis.SpamReason
}

func (wd *Telega) gigaClient(chatID int64, authKey string) *giga.Client {
	if client, ok := wd.pool.Load(chatID); ok {
		return client.(*giga.Client)
	}

	client, _ := giga.NewGigaClient(wd.ctx, authKey)
	wd.pool.Store(chatID, client)

	return client
}

func (wd *Telega) deleteSpam(user *tgbotapi.User, reason string, messageID int, chatID int64) {
	wd.logger.Info(fmt.Sprintf("chatID: %d, удален спам от пользователя %s\n", chatID, user.String()))

	wd.DeleteMessage(chatID, messageID)

	usrName := wd.UserString(user)
	msg, err := wd.SendMsg(fmt.Sprintf("%s, я удалил ваше сообщение, подозрение на спам. \n\n(%s)\n", usrName, reason), "", chatID, Buttons{})
	if err != nil {
		wd.logger.Info(errors.Wrap(err, "send msg error").Error())
		return
	}

	time.Sleep(time.Second * 10)
	wd.DeleteMessage(chatID, msg.MessageID)
}

// Buttons

func (t Buttons) createButtons(msg tgbotapi.Chattable, callback map[string]func(tgbotapi.Update) bool, cancel context.CancelFunc, countColum int) {
	keyboard := tgbotapi.InlineKeyboardMarkup{}
	var Buttons []tgbotapi.InlineKeyboardButton

	switch v := msg.(type) {
	case *tgbotapi.EditMessageTextConfig:
		v.ReplyMarkup = &keyboard
	case *tgbotapi.EditMessageCaptionConfig:
		v.ReplyMarkup = &keyboard
	case *tgbotapi.MessageConfig:
		v.ReplyMarkup = &keyboard
	case *tgbotapi.PhotoConfig:
		v.ReplyMarkup = &keyboard
	}

	for i, _ := range t {
		currentButton := t[i]

		handler := currentButton.handler
		if currentButton.ID == "" {
			UUID, _ := uuid.NewV4()
			currentButton.ID = UUID.String()
		}

		callback[currentButton.ID] = func(update tgbotapi.Update) bool {
			if handler != nil {
				return handler(&update, currentButton)
			}
			return false
		}

		caption := currentButton.caption
		if !currentButton.start.IsZero() {
			second := int(currentButton.start.Sub(time.Now()).Seconds())
			caption = fmt.Sprintf("%s (%02d:%02d:%02d)", currentButton.caption, second/3600, (second%3600)/60, second%60)
		}

		btn := tgbotapi.NewInlineKeyboardButtonData(caption, currentButton.ID)
		Buttons = append(Buttons, btn)
	}

	keyboard.InlineKeyboard = t.breakButtonsByColum(Buttons, countColum)
}

func (t Buttons) breakButtonsByColum(Buttons []tgbotapi.InlineKeyboardButton, countColum int) [][]tgbotapi.InlineKeyboardButton {
	end := 0
	var result [][]tgbotapi.InlineKeyboardButton

	for i := 1; i <= int(float64(len(Buttons)/countColum)); i++ {
		end = i * countColum
		start := (i - 1) * countColum
		if end > len(Buttons) {
			end = len(Buttons)
		}

		row := tgbotapi.NewInlineKeyboardRow(Buttons[start:end]...)
		result = append(result, row)
	}
	if len(Buttons)%countColum > 0 {
		row := tgbotapi.NewInlineKeyboardRow(Buttons[end:len(Buttons)]...)
		result = append(result, row)
	}

	return result
}

func getngrokWebhookURL() string {
	// файл Ngrok должен лежать рядом с основным файлом бота
	currentDir, _ := os.Getwd()
	ngrokpath := filepath.Join(currentDir, "ngrok", "ngrok.exe")
	if _, err := os.Stat(ngrokpath); os.IsNotExist(err) {
		return ""
	}

	err := make(chan error)
	result := make(chan string)

	// горутина для запуска ngrok
	go func(chanErr chan<- error) {
		cmd := exec.Command(ngrokpath, "http", port)
		err := cmd.Run()
		if err != nil {
			errText := fmt.Sprintf("Произошла ошибка запуска:\n err:%v \n", err.Error())

			if cmd.Stderr != nil {
				if stderr := cmd.Stderr.(*bytes.Buffer).String(); stderr != "" {
					errText += fmt.Sprintf("StdErr:%v", stderr)
				}
			}
			chanErr <- fmt.Errorf(errText)
			close(chanErr)
		}
	}(err)

	type ngrokAPI struct {
		Tunnels []*struct {
			PublicUrl string `json:"public_url"`
		} `json:"tunnels"`
	}

	// горутина для получения адреса
	go func(result chan<- string, chanErr chan<- error) {
		// задумка такая, в горутине выше стартует Ngrok, после запуска поднимается вебсервер на порту 4040
		// и я могу получать url через api. Однако, в текущей горутине я не знаю стартанут там Ngrok или нет, по этому таймер
		// продуем подключиться 5 раз (5 сек) если не получилось, ошибка.
		tryCount := 5
		timer := time.NewTicker(time.Second * 1)
		for range timer.C {
			resp, err := http.Get("http://localhost:4040/api/tunnels")
			if (err == nil && !(resp.StatusCode >= http.StatusOK && resp.StatusCode <= http.StatusIMUsed)) || err != nil {
				if tryCount--; tryCount <= 0 {
					chanErr <- fmt.Errorf("Не удалось получить данные ngrok")
					close(chanErr)
					timer.Stop()
					return
				}
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var ngrok = new(ngrokAPI)
			err = json.Unmarshal(body, &ngrok)
			if err != nil {
				chanErr <- err
				close(chanErr)
				timer.Stop()
				return
			}
			if len(ngrok.Tunnels) == 0 {
				chanErr <- fmt.Errorf("Не удалось получить тунели ngrok")
				close(chanErr)
				timer.Stop()
				return
			}
			for _, url := range ngrok.Tunnels {
				if strings.Index(strings.ToLower(url.PublicUrl), "https") >= 0 {
					result <- url.PublicUrl
					close(result)
					timer.Stop()
					return
				}

			}
			chanErr <- fmt.Errorf("Не нашли https тунель ngrok")
			close(chanErr)
		}
	}(result, err)

	select {
	case e := <-err:
		log.Println(e)
		return ""
	case r := <-result:
		return r
	}
}

func downloadFile(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("StatusCode 200 is expected, current %d", resp.StatusCode)
	}

	f, err := os.CreateTemp("", "*.jpg")
	if err != nil {
		return "", err
	}

	_, err = io.Copy(f, resp.Body)
	f.Close()

	return f.Name(), err
}

func (wd *Telega) getDelay() (result time.Duration) {
	result = time.Second

	defer func() {
		recover() // что б не прервалось, result у нас уже инициализирован дефолтным значением
	}()

	if items, err := wd.r.Items(keyActiveMSG); err != nil {
		return result
	} else {
		delay := len(items) / 25 // одновременных изменения не должны быть чаще 30 в сек, по этому задержку устанавливаем с учетом активных сообщений (25 эт с запасом)
		if delay > 1 {
			return time.Duration(delay)
		}
	}
	return
}

func (wd *Telega) StartVoting(sourceMsg *tgbotapi.Message, chatID int64, countVote int) {
	user := sourceMsg.ReplyToMessage.From
	msg := fmt.Sprintf("Что сделать с %s %s (@%s)?", user.FirstName, user.LastName, user.UserName)

	b := Buttons{}
	var sentMessage *tgbotapi.Message

	captionBan, captionRO, captionF := "Бан", "RO на 24ч", "Простить"
	ban, ro, forgive := map[int64]string{}, map[int64]string{}, map[int64]string{}
	var author int64
	var err error

	clear := func(userID int64) {
		delete(ban, userID)
		delete(ro, userID)
		delete(forgive, userID)
	}

	renderCaption := func(buttons Buttons) {
		for _, b := range buttons {
			if strings.HasPrefix(b.caption, captionBan) {
				b.caption = fmt.Sprintf("%s (%d/%d)", captionBan, len(ban), countVote)
			}
			if strings.HasPrefix(b.caption, captionRO) {
				b.caption = fmt.Sprintf("%s (%d/%d)", captionRO, len(ro), countVote)
			}
			if strings.HasPrefix(b.caption, captionF) {
				b.caption = fmt.Sprintf("%s (%d/%d)", captionF, len(forgive), countVote)
			}
		}

		wd.EditButtons(sentMessage, b)
	}

	hBan := func(u *tgbotapi.Update, currentButton *Button) bool {
		clear(u.CallbackQuery.From.ID)
		ban[u.CallbackQuery.From.ID] = fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName)
		if len(ban) >= countVote {
			wd.DeleteMessage(chatID, sourceMsg.MessageID)
			wd.DeleteMessage(chatID, sourceMsg.ReplyToMessage.MessageID)
			wd.kickChatMember(chatID, user.ID)

			wd.logger.Debug(fmt.Sprintf("Ban - %v", ban))
			return true
		}

		renderCaption(b)
		return false
	}
	hRO := func(u *tgbotapi.Update, currentButton *Button) bool {
		clear(u.CallbackQuery.From.ID)
		ro[u.CallbackQuery.From.ID] = fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName)
		if len(ro) >= countVote {
			wd.DeleteMessage(chatID, sourceMsg.MessageID)
			wd.DeleteMessage(chatID, sourceMsg.ReplyToMessage.MessageID)

			wd.DisableSendMessages(chatID, user.ID, time.Hour*24)

			wd.logger.Debug(fmt.Sprintf("RO - %v", ro))
			return true
		}

		renderCaption(b)
		return false
	}

	b = Buttons{
		{
			caption: captionBan,
			handler: hBan,
		},
		{
			caption: captionRO,
			handler: hRO,
		},
		{
			caption: captionF,
			handler: func(u *tgbotapi.Update, currentButton *Button) bool {
				clear(u.CallbackQuery.From.ID)
				forgive[u.CallbackQuery.From.ID] = fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName)
				renderCaption(b)

				if len(forgive) >= countVote {
					_ = wd.DeleteMessage(chatID, sourceMsg.MessageID)

					wd.logger.Debug(fmt.Sprintf("forgive - %v", forgive))
					return true
				}

				return false
			},
		},
		{
			caption: "Я передумал",
			handler: func(update *tgbotapi.Update, button *Button) bool {
				from := wd.GetUser(update)
				if from.ID != author {
					wd.AnswerCallbackQuery(update.CallbackQuery.ID, "Отменить может только автор")
					return false
				}

				wd.DeleteMessage(chatID, sourceMsg.MessageID)
				return true
			},
		},
	}

	if sentMessage, err = wd.SendMsg(msg, "", chatID, b); err == nil {
		author = sourceMsg.From.ID
	}
}

func (wd *Telega) kickChatMemberUntil(chatID, userID int64, untilDate int64) error {
	conf := tgbotapi.KickChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: chatID,
			UserID: userID,
		},
		UntilDate: untilDate,
	}

	_, err := wd.bot.Request(conf)
	return err
}

func (wd *Telega) kickChatMember(chatID, userID int64) error {
	return wd.kickChatMemberUntil(chatID, userID, 0)
}

func (wd *Telega) unbanChatMember(chatID, userID int64) error {
	conf := tgbotapi.UnbanChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: chatID,
			UserID: userID,
		},
		OnlyIfBanned: true,
	}
	_, err := wd.bot.Request(conf)
	return err
}

func (wd *Telega) DeleteMessage(chatID int64, messageID int) error {
	conf := tgbotapi.DeleteMessageConfig{
		ChatID:    chatID,
		MessageID: messageID,
	}

	_, err := wd.bot.Request(conf)
	return err
}

func (wd *Telega) SaveMember(chatID int64, user *tgbotapi.User) {
	wd.mx.Lock()
	defer wd.mx.Unlock()

	if _, ok := wd.users[chatID]; !ok {
		wd.users[chatID] = map[int64]UserInfo{
			user.ID: {
				ID:     user.ID,
				Name:   wd.UserString(user),
				Weight: 1,
			},
		}
	} else {
		wd.users[chatID][user.ID] = UserInfo{
			ID:     user.ID,
			Name:   wd.UserString(user),
			Weight: wd.users[chatID][user.ID].Weight + 1,
		}
	}
}

func (wd *Telega) UserString(user *tgbotapi.User) string {
	userName := ""
	if user.UserName != "" {
		userName = "@" + user.UserName
	} else {
		userName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
	}

	return userName
}

func (wd *Telega) CastUserToUserinfo(tUser *tgbotapi.User) *UserInfo {
	if tUser == nil {
		return nil
	}

	return &UserInfo{ID: tUser.ID, Name: wd.UserString(tUser)}
}

func (wd *Telega) GetRandUserByWeight(chatID, excludeUserID int64) (result *UserInfo) {
	wd.mx.RLock()
	defer wd.mx.RUnlock()

	usersByChat, ok := wd.users[chatID]
	if !ok || len(usersByChat) == 0 {
		return nil
	}

	allUsers := slices.Collect(maps.Values(usersByChat))

	// отсекаем пользователей с весом 0, 1
	allUsers = lo.Filter[UserInfo](allUsers, func(item UserInfo, _ int) bool {
		return item.Weight > 2
	})

	wd.logger.WithGroup("GetRandUserByWeight").With("chatID", chatID).Debug(fmt.Sprintf("users len - %d", len(allUsers)))

	//data, _ := json.Marshal(&usersByChat)
	//fmt.Println("usersByChat - ", string(data))

	tmp := make([]UserInfo, 0, len(allUsers))
	prefixSums := make([]int64, len(allUsers))

	// перекладываем в слайс, тут же находим сумму всех элементов
	for _, v := range allUsers {
		if excludeUserID > 0 && v.ID == excludeUserID {
			continue
		}

		tmp = append(tmp, v)
	}

	prefixSums[0] = tmp[0].Weight
	for i := 1; i < len(tmp); i++ {
		prefixSums[i] = prefixSums[i-1] + tmp[i].Weight
	}

	last := int(prefixSums[len(prefixSums)-1])
	if last == 0 {
		return &tmp[rand.Intn(len(tmp))] // если веса не заданы, обычный рандом
	}

	randomValue := rand.Intn(last)
	for i, v := range prefixSums {
		if randomValue < int(v) {
			return &tmp[i]
		}
	}

	return nil
}

func (wd *Telega) GetRandUser(chatID, excludeUserID int64) (result *UserInfo) {
	wd.mx.RLock()
	defer wd.mx.RUnlock()

	usersByChat, ok := wd.users[chatID]
	if !ok {
		return nil
	}

	for k, v := range usersByChat {
		if excludeUserID > 0 && k == excludeUserID {
			continue
		}

		return &v
	}

	return
}

func (wd *Telega) AnswerCallbackQuery(callbackQueryID, txt string) error {
	conf := tgbotapi.CallbackConfig{
		CallbackQueryID: callbackQueryID,
		Text:            txt,
		ShowAlert:       true,
	}

	_, err := wd.bot.Request(conf)
	return err
}

func (wd *Telega) EditButtons(msg *tgbotapi.Message, buttons Buttons) {
	var editmsg tgbotapi.Chattable
	if msg.Caption != "" {
		teditmsg := tgbotapi.NewEditMessageCaption(msg.Chat.ID, msg.MessageID, msg.Caption)
		teditmsg.ParseMode = "HTML"
		editmsg = &teditmsg
	} else {
		teditmsg := tgbotapi.NewEditMessageText(msg.Chat.ID, msg.MessageID, msg.Text)
		teditmsg.ParseMode = "HTML"
		editmsg = &teditmsg
	}

	buttons.createButtons(editmsg, wd.callback, func() {}, 2)
	wd.bot.Send(editmsg)
}

func (wd *Telega) Shutdown() {
	wd.storeUsersInfo()

	wd.mx.RLock()
	wd.r.SetMap(lastMsgKey, wd.lastMsg)
	wd.mx.RUnlock()

	wd.cancel()

	wd.bot.StopReceivingUpdates()
	ctx, _ := context.WithTimeout(context.Background(), time.Second*5)
	err := wd.httpServer.Shutdown(ctx)
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "http server shutdown error").Error())
	}
}

func (wd *Telega) restoreUsersInfo() {
	wd.mx.Lock()
	defer wd.mx.Unlock()

	data, err := wd.r.Get(userInfo)
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "restoreUsersInfo error").Error())
		return
	}

	err = json.Unmarshal([]byte(data), &wd.users)
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "restoreUsersInfo error").Error())
		return
	}
}

func (wd *Telega) watchActiveRandomModerator(delay time.Duration) {
	defer func() {
		if e := recover(); e != nil {
			wd.logger.Error(fmt.Sprintf("PANIC: %v", e))
		}
	}()

	for {
		for _, k := range wd.r.KeysMask(keyActiveRandomModerator + "*") {
			v, err := wd.r.Get(k)
			if err != nil {
				continue
			}

			data := make(map[string]interface{})
			if err := json.Unmarshal([]byte(v), &data); err != nil {
				continue
			}

			deadline, _ := time.Parse(time.RFC1123, data["Deadline"].(string))
			if time.Now().After(deadline) {
				ID := data["User"].(map[string]interface{})["ID"].(float64)
				chatID := data["ChatID"].(float64)
				wd.RemoveModerator(int64(chatID), int64(ID))
			}
		}

		select {
		case <-time.After(delay):
		case <-wd.ctx.Done():
			return
		}
	}
}

func (wd *Telega) watchKilledUsers(delay time.Duration) {
	for {
		killed, _ := wd.r.Items(killedUsers)
		for _, data := range killed {
			tmp := new(KilledInfo)
			if err := json.Unmarshal([]byte(data), tmp); err == nil {
				if time.Now().After(tmp.To) {
					wd.r.DeleteItems(killedUsers, data)
				}
			}
		}

		select {
		case <-time.After(delay):
		case <-wd.ctx.Done():
			return
		}
	}
}

func (wd *Telega) storeUsersInfo() {
	wd.mx.RLock()
	defer wd.mx.RUnlock()

	data, err := json.Marshal(wd.users)
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "storeUsersInfo error").Error())
		return
	}

	err = wd.r.Set(userInfo, string(data), -1)
	if err != nil {
		wd.logger.Error(errors.Wrap(err, "storeUsersInfo error").Error())
		return
	}
}

func In[T comparable](value T, array []T) bool {
	for _, item := range array {
		if item == value {
			return true
		}
	}
	return false
}

func (k *KilledInfo) String() string {
	d, err := json.Marshal(k)
	if err != nil {
		return ""
	}

	return string(d)
}

func (wd *Telega) CheckAndBlockMember(chatID int64, appendedUser *tgbotapi.User, conf *Conf) bool {
	if conf.BlockMembers.UserNameRegExp == "" || appendedUser == nil {
		return false
	}

	name := appendedUser.FirstName
	if appendedUser.LastName != "" {
		name += " " + appendedUser.LastName
	}

	exp := conf.BlockMembers.UserNameRegExp

	if wd.checkRegExp(appendedUser.String(), exp) || wd.checkRegExp(name, exp) {
		wd.kickChatMember(chatID, appendedUser.ID)
		wd.logger.Info(fmt.Sprintf("пользователь %q был заблокирован в соответствии с настройками \"blockMembers\"\n", appendedUser.String()))
		return true
	}

	return false
}

func (wd *Telega) checkRegExp(userName string, exp string) bool {
	var re = regexp.MustCompile(exp)
	match := re.FindAllString(userName, -1)
	return len(match) > 0
}

func (u *UserInfo) String() string {
	d, err := json.Marshal(u)
	if err != nil {
		return ""
	}

	return string(d)
}

func (wd *Telega) userWeight(chatID, userID int64) int64 {
	if usersByChat, ok := wd.users[chatID]; !ok {
		wd.logger.Info(fmt.Sprintf("chatID: %d not found in map", chatID))
		return 0
	} else if user, ok := usersByChat[userID]; !ok {
		wd.logger.Info(fmt.Sprintf("userID: %d not found in map (chatID: %d)", userID, chatID))
		return 0
	} else {
		return user.Weight
	}
}
