package app

import (
	"Antispam/giga"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	uuid "github.com/nu7hatch/gouuid"
)

type UserInfo struct {
	ID   int64
	Name string
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
	bot        *tgbotapi.BotAPI
	callback   map[string]func(tgbotapi.Update) bool
	hooks      map[string]func(tgbotapi.Update) bool
	running    int32
	r          IRedis //*Redis
	r2         *Redis // todo: не красиво, взаимодействие с базой надо через адаптер
	gClient    *giga.Client
	one        sync.Once
	lastMsg    map[string]string // для хранения последнего сообщения по пользователю
	mx         sync.RWMutex
	httpServer *http.Server
	users      map[int64]map[int64]UserInfo
	ctx        context.Context
	cancel     context.CancelFunc
}

type KilledInfo struct {
	UserID   int64
	UserName string
	To       time.Time
}

func (t *Telega) New(debug bool, certFilePath string, pollingMode bool) (result tgbotapi.UpdatesChannel, err error) {
	t.callback = map[string]func(tgbotapi.Update) bool{}
	t.hooks = map[string]func(tgbotapi.Update) bool{}
	t.users = map[int64]map[int64]UserInfo{}

	t.r, err = new(Redis).Create(redisAddr)
	if err != nil {
		return nil, errors.Wrap(err, "create redis")
	}

	// восстанавливаем данные из БД
	t.mx.Lock()
	t.lastMsg, _ = t.r.StringMap(lastMsgKey)
	t.mx.Unlock()

	t.ctx, t.cancel = context.WithCancel(context.Background())

	t.restoreUsersInfo()
	go t.watchKilledUsers(time.Minute)

	t.bot, err = tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, err
	}
	if webhookURL == "" && !pollingMode {
		// для отладки получаем через ngrok
		if webhookURL = getngrokWebhookURL(); webhookURL == "" {
			return nil, errors.New("не удалось получить WebhookURL")
		}
	}

	t.bot.Debug = debug
	t.httpServer = &http.Server{Addr: ":" + port}
	go t.httpServer.ListenAndServe()

	go t.watchActiveRandomModerator(time.Minute)

	fmt.Printf("listen port: %s, debug: %v\n", port, debug)

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
		_, err = t.bot.Request(wh)
		if err != nil {
			return nil, errors.Wrap(err, "request error")
		}

		return t.bot.ListenForWebhook("/"), nil // вебхук
	} else {
		_, err = t.bot.Request(&tgbotapi.DeleteWebhookConfig{}) // при использовании полинга
		u := tgbotapi.NewUpdate(0)
		u.Timeout = 60
		u.AllowedUpdates = []string{"message", "chat_member", "callback_query", "chat_join_request", "my_chat_member"}
		return t.bot.GetUpdatesChan(u), nil // полинг
	}
}

func (t *Telega) SendTTLMsg(msg string, imgURL string, chatID int64, buttons Buttons, ttl time.Duration) (*tgbotapi.Message, error) {
	if m, err := t.SendMsg(msg, imgURL, chatID, buttons); err != nil {
		return nil, err
	} else {
		go func() {
			time.Sleep(ttl)
			t.DeleteMessage(chatID, m.MessageID)
		}()

		return m, nil
	}
}

func (t *Telega) SendMsg(msg string, imgURL string, chatID int64, buttons Buttons) (*tgbotapi.Message, error) {
	if imgURL != "" {
		if path, err := downloadFile(imgURL); err == nil {
			defer os.RemoveAll(path)

			newmsg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(path))
			newmsg.Caption = msg
			newmsg.ParseMode = "HTML"
			return t.createButtonsAndSend(&newmsg, buttons)
		} else {
			return &tgbotapi.Message{}, err
		}
	} else {
		newmsg := tgbotapi.NewMessage(chatID, msg)
		newmsg.ParseMode = "HTML"
		return t.createButtonsAndSend(&newmsg, buttons)
	}
}

func (t *Telega) getAllChats() []string {
	var result []string

	for _, key := range t.r.Keys() {
		if title, err := t.r.Get(key); err == nil {
			result = append(result, title)
		}
	}

	return result
}

func (t *Telega) ReplyMsg(msg string, imgURL string, chatID int64, buttons Buttons, parrentMessageID int) (*tgbotapi.Message, error) {
	if imgURL != "" {
		if path, err := downloadFile(imgURL); err == nil {
			defer os.RemoveAll(path)

			newmsg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(path))
			newmsg.ReplyToMessageID = parrentMessageID
			newmsg.Caption = msg
			newmsg.ParseMode = "HTML"
			return t.createButtonsAndSend(&newmsg, buttons)
		} else {
			return &tgbotapi.Message{}, err
		}
	} else {
		newmsg := tgbotapi.NewMessage(chatID, msg)
		newmsg.ReplyToMessageID = parrentMessageID
		newmsg.ParseMode = "HTML"
		return t.createButtonsAndSend(&newmsg, buttons)
	}
}

func (t *Telega) SendFile(chatID int64, filepath string) error {
	msg := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filepath))
	_, err := t.bot.Send(msg)
	return err
}

func (t *Telega) createButtonsAndSend(msg tgbotapi.Chattable, buttons Buttons) (*tgbotapi.Message, error) {
	// if _, ok := msg.(tgbotapi.Fileable); ok {
	//	fmt.Println(1)
	// }

	if len(buttons) > 0 {
		buttons.createButtons(msg, t.callback, func() {}, 2)
	}

	//timerExist := false
	//for _, b := range buttons {
	//	if timerExist = b.timer > 0; timerExist {
	//		break
	//	}
	//}

	m, err := t.bot.Send(msg)

	// Отключен таймер на кнопке т.к. при большом количествет присоединившихся пользователях не будет работать
	// if timerExist {
	// 	go t.setTimer(m, buttons, cxt, cancel) // таймер кнопки
	// }

	return &m, err
}

func (t *Telega) EditMsg(msg *tgbotapi.Message, txt string, buttons Buttons) *tgbotapi.Message {
	editmsg := tgbotapi.NewEditMessageText(msg.Chat.ID, msg.MessageID, txt)
	editmsg.ParseMode = "HTML"
	m, _ := t.createButtonsAndSend(&editmsg, buttons)

	return m
}

func (t *Telega) MeIsAdmin(chatConfig tgbotapi.ChatConfig) bool {
	me, _ := t.bot.GetMe()
	return t.UserIsAdmin(chatConfig, me.ID)
}

func (t *Telega) getChatAdministrators(chatConfig tgbotapi.ChatConfig) []tgbotapi.ChatMember {
	admins, _ := t.bot.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{ChatConfig: chatConfig})
	return admins
}

func (t *Telega) UserIsAdmin(chatConfig tgbotapi.ChatConfig, userID int64) bool {
	for _, a := range t.getChatAdministrators(chatConfig) {
		if a.User.ID == userID {
			return true
		}
	}

	return false
}

func (t *Telega) AppointModerator(chatID int64, user *UserInfo, deadline time.Time) error {
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

	_, err := t.bot.MakeRequest(promoteChatMember, params)
	if err == nil {
		t.storeNewModerator(chatID, user, deadline)
	}

	return err
}

func (t *Telega) RemoveModerator(chatID, userID int64) error {
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

	_, err := t.bot.MakeRequest(promoteChatMember, params)
	if err == nil {
		t.r.Delete(keyActiveRandomModerator + params["chat_id"])
	}

	return err
}

func (t *Telega) storeNewModerator(chatID int64, user *UserInfo, deadline time.Time) {
	t.mx.Lock()
	defer t.mx.Unlock()

	data := map[string]interface{}{
		"ChatID":   chatID,
		"User":     user,
		"Deadline": deadline.Format(time.RFC1123),
	}

	d, err := json.Marshal(&data)
	if err != nil {
		return
	}

	t.r.Set(keyActiveRandomModerator+strconv.FormatInt(chatID, 10), string(d), -1)
}

func (t *Telega) GetActiveRandModerator(chatID int64) (string, time.Time) {
	t.mx.RLock()
	defer t.mx.RUnlock()

	defer func() {
		if e := recover(); e != nil {
			fmt.Println("PANIC:", e)
		}
	}()

	v, err := t.r.Get(keyActiveRandomModerator + strconv.FormatInt(chatID, 10))
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

func (t *Telega) UserIsCreator(chatConfig tgbotapi.ChatConfig, userID int64) bool {
	for _, a := range t.getChatAdministrators(chatConfig) {
		if a.IsCreator() && a.User.ID == userID {
			return true
		}
	}

	return false
}

func (t *Telega) setTimer(msg tgbotapi.Message, buttons Buttons, cxt context.Context, cancel context.CancelFunc) {
	tick := time.NewTicker(t.getDelay())
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

			t.EditButtons(&msg, buttons)

			if button != nil {
				if button.handler != nil {
					button.handler(nil, button)
				}

				delete(t.callback, button.ID)
				break B
			}

			<-tick.C
		}
	}
}

func (t *Telega) CallbackQuery(update tgbotapi.Update) bool {
	if update.CallbackQuery == nil || update.CallbackQuery.Message == nil {
		return false
	}

	if call, ok := t.callback[update.CallbackQuery.Data]; ok {
		if call != nil {
			if call(update) {
				t.DeleteMessage(update.CallbackQuery.Message.Chat.ID, update.CallbackQuery.Message.MessageID)
				delete(t.callback, update.CallbackQuery.Data)
			}
		}
	}

	return true
}

func (t *Telega) downloadFile(filepath, url string) error {
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

func (t *Telega) GetUser(update *tgbotapi.Update) *tgbotapi.User {
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

func (t *Telega) GetMessage(update tgbotapi.Update) *tgbotapi.Message {
	if update.Message != nil {
		return update.Message
	} else if update.CallbackQuery != nil {
		return update.CallbackQuery.Message
	} else {
		return nil
	}
}

func (t *Telega) ReadFile(message *tgbotapi.Message) (data string, err error) {
	// message.Chat.ID
	downloadFilebyID := func(FileID string) {
		var file tgbotapi.File
		if file, err = t.bot.GetFile(tgbotapi.FileConfig{FileID}); err == nil {
			_, fileName := path.Split(file.FilePath)
			filePath := path.Join(os.TempDir(), fileName)
			defer os.Remove(filePath)

			err = t.downloadFile(filePath, file.Link(botToken))
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

func (t *Telega) DisableSendMessages(chatID int64, userID int64, duration time.Duration) {
	t.restrictChatMemberConfig(chatID, userID, duration, false)
}

func (t *Telega) EnableWritingMessages(chatID int64, userID int64) {
	t.restrictChatMemberConfig(chatID, userID, 0, true)
}

func (t *Telega) restrictChatMemberConfig(chatID int64, userID int64, duration time.Duration, allow bool) {
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

	_, err := t.bot.Request(conf)
	if err != nil {
		log.Printf("При изменении прав пользователя произошла ошибка: %v\n", err)
	}
}

func (t *Telega) KickChatMember(chatID int64, user tgbotapi.User) {
	go func() {
		userName := t.UserString(&user)
		users, err := t.r.Items(keyUsingOneTry)
		if t.secondAttempt(users, err, strconv.FormatInt(user.ID, 10)) {
			t.kickChatMember(chatID, user.ID)
			t.r.DeleteItems(keyUsingOneTry, strconv.FormatInt(user.ID, 10))
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

func (t *Telega) secondAttempt(users []string, err error, userID string) bool {
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

func (t *Telega) deleteLastMsg(userID int64) {
	t.mx.Lock()
	defer t.mx.Unlock()

	delete(t.lastMsg, strconv.FormatInt(userID, 10))
}

func (t *Telega) deleteAllLastMsg() {
	t.mx.Lock()
	defer t.mx.Unlock()

	t.lastMsg = map[string]string{}
}

func (t *Telega) IsSPAM(userID int64, msg string, conf *Conf) (bool, string) {
	if conf == nil {
		return false, ""
	}

	t.mx.Lock()
	defer t.mx.Unlock()

	// если есть последнее сообщение тогда выходим, не проверяем
	if _, ok := t.lastMsg[strconv.FormatInt(userID, 10)]; ok {
		return false, ""
	}

	c := t.gigaClient(conf.AI.GigaChat.AuthKey)
	s, p, r, err := c.GetSpamPercent(strings.ReplaceAll(msg, "\n", " "))
	if err != nil {
		log.Println(err)
	}

	log.Printf("msg: %s\n"+
		"\tsolution: %v\n"+
		"\tpercent: %d\n"+
		"\treason: %s\n\n", msg, s, p, r)

	if !s && err == nil {
		t.lastMsg[strconv.FormatInt(userID, 10)] = msg
	}

	return s, r
}

func (t *Telega) gigaClient(authKey string) *giga.Client {
	t.one.Do(func() {
		t.gClient, _ = giga.NewGigaClient(context.Background(), authKey)
	})

	return t.gClient
}

func (t *Telega) deleteSpam(user *tgbotapi.User, reason string, messageID int, chatID int64) {
	t.DeleteMessage(chatID, messageID)

	usrName := t.UserString(user)
	msg, err := t.SendMsg(fmt.Sprintf("%s, я удалил ваше сообщение, подозрение на спам. \n\n(%s)\n", usrName, reason), "", chatID, Buttons{})
	if err != nil {
		log.Println("ERROR:", errors.Wrap(err, "send msg error"))
		return
	}

	time.Sleep(time.Second * 10)
	t.DeleteMessage(chatID, msg.MessageID)
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

func (t *Telega) getDelay() (result time.Duration) {
	result = time.Second

	defer func() {
		recover() // что б не прервалось, result у нас уже инициализирован дефолтным значением
	}()

	if items, err := t.r.Items(keyActiveMSG); err != nil {
		return result
	} else {
		delay := len(items) / 25 // одновременных изменения не должны быть чаще 30 в сек, по этому задержку устанавливаем с учетом активных сообщений (25 эт с запасом)
		if delay > 1 {
			return time.Duration(delay)
		}
	}
	return
}

func (t *Telega) StartVoting(sourceMsg *tgbotapi.Message, chatID int64, countVote int) {
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

		t.EditButtons(sentMessage, b)
	}

	hBan := func(u *tgbotapi.Update, currentButton *Button) bool {
		clear(u.CallbackQuery.From.ID)
		ban[u.CallbackQuery.From.ID] = fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName)
		if len(ban) >= countVote {
			t.DeleteMessage(chatID, sourceMsg.MessageID)
			t.DeleteMessage(chatID, sourceMsg.ReplyToMessage.MessageID)
			t.kickChatMember(chatID, user.ID)

			log.Println("Ban -", ban)
			return true
		}

		renderCaption(b)
		return false
	}
	hRO := func(u *tgbotapi.Update, currentButton *Button) bool {
		clear(u.CallbackQuery.From.ID)
		ro[u.CallbackQuery.From.ID] = fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName)
		if len(ro) >= countVote {
			t.DeleteMessage(chatID, sourceMsg.MessageID)
			t.DeleteMessage(chatID, sourceMsg.ReplyToMessage.MessageID)

			t.DisableSendMessages(chatID, user.ID, time.Hour*24)

			log.Println("RO -", ro)
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
					_ = t.DeleteMessage(chatID, sourceMsg.MessageID)

					log.Println("forgive -", forgive)
					return true
				}

				return false
			},
		},
		{
			caption: "Я передумал",
			handler: func(update *tgbotapi.Update, button *Button) bool {
				from := t.GetUser(update)
				if from.ID != author {
					t.AnswerCallbackQuery(update.CallbackQuery.ID, "Отменить может только автор")
					return false
				}

				t.DeleteMessage(chatID, sourceMsg.MessageID)
				return true
			},
		},
	}

	if sentMessage, err = t.SendMsg(msg, "", chatID, b); err == nil {
		author = sourceMsg.From.ID
	}
}

func (t *Telega) kickChatMemberUntil(chatID, userID int64, untilDate int64) error {
	conf := tgbotapi.KickChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: chatID,
			UserID: userID,
		},
		UntilDate: untilDate,
	}

	_, err := t.bot.Request(conf)
	return err
}

func (t *Telega) kickChatMember(chatID, userID int64) error {
	return t.kickChatMemberUntil(chatID, userID, 0)
}

func (t *Telega) unbanChatMember(chatID, userID int64) error {
	conf := tgbotapi.UnbanChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: chatID,
			UserID: userID,
		},
		OnlyIfBanned: true,
	}
	_, err := t.bot.Request(conf)
	return err
}

func (t *Telega) DeleteMessage(chatID int64, messageID int) error {
	conf := tgbotapi.DeleteMessageConfig{
		ChatID:    chatID,
		MessageID: messageID,
	}

	_, err := t.bot.Request(conf)
	return err
}

func (t *Telega) SaveMember(chatID int64, user *tgbotapi.User) {
	if _, ok := t.users[chatID]; !ok {
		t.users[chatID] = map[int64]UserInfo{
			user.ID: {
				ID:   user.ID,
				Name: t.UserString(user),
			},
		}
	} else {
		t.users[chatID][user.ID] = UserInfo{
			ID:   user.ID,
			Name: t.UserString(user),
		}
	}
}

func (t *Telega) UserString(user *tgbotapi.User) string {
	userName := ""
	if user.UserName != "" {
		userName = "@" + user.UserName
	} else {
		userName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
	}

	return userName
}

func (t *Telega) CastUserToUserinfo(tUser *tgbotapi.User) *UserInfo {
	if tUser == nil {
		return nil
	}

	return &UserInfo{ID: tUser.ID, Name: t.UserString(tUser)}
}

func (t *Telega) GetRandUser(chatID, excludeUserID int64) (result *UserInfo) {
	t.mx.RLock()
	defer t.mx.RUnlock()

	for k, v := range t.users {
		if k != chatID {
			continue
		}

		for k2, v2 := range v {
			if k2 == excludeUserID {
				continue
			}

			return &v2
		}
	}

	return
}

func (t *Telega) AnswerCallbackQuery(callbackQueryID, txt string) error {
	conf := tgbotapi.CallbackConfig{
		CallbackQueryID: callbackQueryID,
		Text:            txt,
		ShowAlert:       true,
	}

	_, err := t.bot.Request(conf)
	return err
}

func (t *Telega) EditButtons(msg *tgbotapi.Message, buttons Buttons) {
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

	buttons.createButtons(editmsg, t.callback, func() {}, 2)
	t.bot.Send(editmsg)
}

func (t *Telega) Shutdown() {
	t.storeUsersInfo()

	t.mx.RLock()
	t.r.SetMap(lastMsgKey, t.lastMsg)
	t.mx.RUnlock()

	t.cancel()

	t.bot.StopReceivingUpdates()
	ctx, _ := context.WithTimeout(context.Background(), time.Second*5)
	err := t.httpServer.Shutdown(ctx)
	if err != nil {
		log.Println("http server shutdown error:", err.Error())
	}
}

func (t *Telega) restoreUsersInfo() {
	t.mx.Lock()
	defer t.mx.Unlock()

	data, err := t.r.Get(userInfo)
	if err != nil {
		log.Println(errors.Wrap(err, "restoreUsersInfo error"))
		return
	}

	err = json.Unmarshal([]byte(data), &t.users)
	if err != nil {
		log.Println(errors.Wrap(err, "restoreUsersInfo error"))
		return
	}
}

func (t *Telega) watchActiveRandomModerator(delay time.Duration) {
	defer func() {
		if e := recover(); e != nil {
			fmt.Println("PANIC:", e)
		}
	}()

	for {
		for _, k := range t.r.KeysMask(keyActiveRandomModerator + "*") {
			v, err := t.r.Get(k)
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
				t.RemoveModerator(int64(chatID), int64(ID))
			}
		}

		select {
		case <-time.After(delay):
		case <-t.ctx.Done():
			return
		}
	}
}

func (t *Telega) watchKilledUsers(delay time.Duration) {
	for {
		killed, _ := t.r.Items(killedUsers)
		for _, data := range killed {
			tmp := new(KilledInfo)
			if err := json.Unmarshal([]byte(data), tmp); err == nil {
				if time.Now().After(tmp.To) {
					t.r.DeleteItems(killedUsers, data)
				}
			}
		}

		select {
		case <-time.After(delay):
		case <-t.ctx.Done():
			return
		}
	}
}

func (t *Telega) storeUsersInfo() {
	t.mx.RLock()
	defer t.mx.RUnlock()

	data, err := json.Marshal(t.users)
	if err != nil {
		log.Println(errors.Wrap(err, "storeUsersInfo error"))
		return
	}

	err = t.r.Set(userInfo, string(data), -1)
	if err != nil {
		log.Println(errors.Wrap(err, "storeUsersInfo error"))
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

func (t *Telega) CheckAndBlockMember(chatID int64, appendedUser *tgbotapi.User, conf *Conf) bool {
	if conf.BlockMembers.UserNameRegExp == "" || appendedUser == nil {
		return false
	}

	var re = regexp.MustCompile(conf.BlockMembers.UserNameRegExp)
	match := re.FindAllString(appendedUser.String(), -1)
	if len(match) > 0 {
		t.kickChatMember(chatID, appendedUser.ID)
		fmt.Printf("пользователь %q был заблокирован в соответствии с настройками \"blockMembers\"\n", appendedUser.String())
		return true
	}

	return false
}

func (u *UserInfo) String() string {
	d, err := json.Marshal(u)
	if err != nil {
		return ""
	}

	return string(d)
}
