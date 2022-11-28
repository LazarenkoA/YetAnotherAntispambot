package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	uuid "github.com/nu7hatch/gouuid"
)

type Button struct {
	caption string
	handler func(*tgbotapi.Update, *Button) bool
	timer   int

	// время начала таймера
	start time.Time
	ID    string
}

const keyActiveMSG = "keyActiveMSG"

type Buttons []*Button

type Telega struct {
	bot      *tgbotapi.BotAPI
	callback map[string]func(tgbotapi.Update) bool
	hooks    map[string]func(tgbotapi.Update) bool
	running  int32
	r        *Redis // todo: не красиво, взаимодействие с базой надо через адаптер
}

func (t *Telega) New() (result tgbotapi.UpdatesChannel, err error) {
	t.callback = map[string]func(tgbotapi.Update) bool{}
	t.hooks = map[string]func(tgbotapi.Update) bool{}
	t.r, _ = new(Redis).Create(redisaddr)

	t.bot, err = tgbotapi.NewBotAPIWithClient(BotToken, new(http.Client))
	// t.bot.Debug = true
	if err != nil {
		return nil, err
	}
	if WebhookURL == "" {
		WebhookURL = getngrokWebhookURL() // для отладки получаем через ngrok
		if WebhookURL == "" {
			return nil, errors.New("не удалось получить WebhookURL")
		}
	}

	_, err = t.bot.SetWebhook(tgbotapi.NewWebhook(WebhookURL))
	if err != nil {
		return nil, err
	}

	go http.ListenAndServe(":"+port, nil)
	return t.bot.ListenForWebhook("/"), nil
}

func (t *Telega) SendMsg(msg string, imgURL string, chatID int64, buttons Buttons) (*tgbotapi.Message, error) {
	if imgURL != "" {
		if path, err := downloadFile(imgURL); err == nil {
			defer os.RemoveAll(path)

			newmsg := tgbotapi.NewPhotoUpload(chatID, path)
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

			newmsg := tgbotapi.NewPhotoUpload(chatID, path)
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
	msg := tgbotapi.NewDocumentUpload(chatID, filepath)
	_, err := t.bot.Send(msg)
	return err
}

func (t *Telega) createButtonsAndSend(msg tgbotapi.Chattable, buttons Buttons) (*tgbotapi.Message, error) {
	// if _, ok := msg.(tgbotapi.Fileable); ok {
	//	fmt.Println(1)
	// }

	buttons.createButtons(msg, t.callback, func() {}, 3)

	timerExist := false
	for _, b := range buttons {
		if timerExist = b.timer > 0; timerExist {
			break
		}
	}

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
	return t.UserIsAdmin(chatConfig, &me)
}

func (t *Telega) UserIsAdmin(chatConfig tgbotapi.ChatConfig, user *tgbotapi.User) bool {
	admins, err := t.bot.GetChatAdministrators(chatConfig)
	if err != nil || len(admins) == 0 {
		return false
	}

	for _, a := range admins {
		if (a.IsAdministrator() || a.IsCreator()) && a.User.ID == user.ID {
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
				t.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
					ChatID:    update.CallbackQuery.Message.Chat.ID,
					MessageID: update.CallbackQuery.Message.MessageID})
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

			err = t.downloadFile(filePath, file.Link(BotToken))
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

func (t *Telega) DisableSendMessages(chatID int64, user *tgbotapi.User, duration time.Duration) {
	denied := false
	var untilDate int64
	if duration > 0 {
		untilDate = time.Now().Add(duration).Unix()
	}

	_, err := t.bot.RestrictChatMember(tgbotapi.RestrictChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: chatID,
			UserID: user.ID,
		},
		UntilDate:       untilDate,
		CanSendMessages: &denied,
	})

	if err != nil {
		log.Printf("При ограничении прав пользователя произошла ошибка: %v\n", err)
	}
}

func (t *Telega) EnableWritingMessages(chatID int64, user *tgbotapi.User) {
	access := true
	t.bot.RestrictChatMember(tgbotapi.RestrictChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: chatID,
			UserID: user.ID,
		},
		CanSendMessages:       &access,
		CanSendMediaMessages:  &access,
		CanSendOtherMessages:  &access,
		CanAddWebPagePreviews: &access,
	})
}

func (t *Telega) KickChatMember(user tgbotapi.User, config tgbotapi.KickChatMemberConfig) {
	const key = "UsingOneTry"
	go func() {
		userName := ""
		if user.UserName != "" {
			userName = "@" + user.UserName
		} else {
			userName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
		}

		users, err := t.r.Items(key)
		if t.secondAttempt(users, err, strconv.Itoa(user.ID)) {
			t.bot.KickChatMember(config)
			t.r.DeleteItems(key, strconv.Itoa(user.ID))
			return
		}

		t.r.AppendItems(key, strconv.Itoa(user.ID))
		msg, _ := t.SendMsg(fmt.Sprintf("%s, мне придется Вас удалить из чата, но у Вас будет еще одна попытка входа.", userName), "", config.ChatID, Buttons{})
		time.Sleep(time.Second * 10)
		t.bot.KickChatMember(config)
		t.bot.UnbanChatMember(tgbotapi.ChatMemberConfig{
			ChatID: config.ChatID,
			UserID: user.ID,
		})

		t.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
			ChatID:    config.ChatID,
			MessageID: msg.MessageID})
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

// Buttons

func (t Buttons) createButtons(msg tgbotapi.Chattable, callback map[string]func(tgbotapi.Update) bool, cancel context.CancelFunc, countColum int) {
	keyboard := tgbotapi.InlineKeyboardMarkup{}
	var Buttons = []tgbotapi.InlineKeyboardButton{}

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
	result := [][]tgbotapi.InlineKeyboardButton{}

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
	ngrokpath := filepath.Join(currentDir, "ngrok.exe")
	if _, err := os.Stat(ngrokpath); os.IsNotExist(err) {
		return ""
	}

	err := make(chan error, 0)
	result := make(chan string, 0)

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
			body, _ := ioutil.ReadAll(resp.Body)
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
	case <-err:
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

	f, err := ioutil.TempFile("", "*.jpg")
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
	votedUsers := map[int]struct{}{}

	userCanVote := func(u *tgbotapi.User) bool {
		if u.ID == user.ID {
			return false
		}
		if _, ok := votedUsers[u.ID]; ok {
			return false
		}
		votedUsers[u.ID] = struct{}{}
		return true
	}

	ban, ro, forgive := []string{}, []string{}, []string{}
	hBan := func(u *tgbotapi.Update, currentButton *Button) bool {
		if !userCanVote(u.CallbackQuery.From) {
			return false
		}

		ban = append(ban, fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName))
		if len(ban) >= countVote {
			t.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
				ChatID:    chatID,
				MessageID: sourceMsg.MessageID})

			t.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
				ChatID:    chatID,
				MessageID: sourceMsg.ReplyToMessage.MessageID})

			t.bot.KickChatMember(tgbotapi.KickChatMemberConfig{
				ChatMemberConfig: tgbotapi.ChatMemberConfig{
					ChatID: chatID,
					UserID: user.ID,
				},
				UntilDate: 0,
			})

			log.Println("Ban -", ban)
			return true
		}

		currentButton.caption = fmt.Sprintf("Бан (%d/%d)", len(ban), countVote)
		t.EditButtons(sentMessage, b)
		return false
	}
	hRO := func(u *tgbotapi.Update, currentButton *Button) bool {
		if !userCanVote(u.CallbackQuery.From) {
			return false
		}

		ro = append(ro, fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName))
		if len(ro) >= countVote {
			t.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
				ChatID:    chatID,
				MessageID: sourceMsg.MessageID})

			t.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
				ChatID:    chatID,
				MessageID: sourceMsg.ReplyToMessage.MessageID})

			t.DisableSendMessages(chatID, user, time.Hour*24)

			log.Println("RO -", ro)
			return true
		}

		currentButton.caption = fmt.Sprintf("RO на день (%d/%d)", len(ro), countVote)
		t.EditButtons(sentMessage, b)
		return false
	}
	b = Buttons{
		{
			caption: "Бан",
			handler: hBan,
		},
		{
			caption: "RO на день",
			handler: hRO,
		},
		{
			caption: "Простить",
			handler: func(u *tgbotapi.Update, currentButton *Button) bool {
				if !userCanVote(u.CallbackQuery.From) {
					return false
				}

				forgive = append(forgive, fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName))
				currentButton.caption = fmt.Sprintf("Простить (%d/%d)", len(forgive), countVote)
				t.EditButtons(sentMessage, b)
				if len(forgive) >= countVote {
					t.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
						ChatID:    chatID,
						MessageID: sourceMsg.MessageID})

					log.Println("forgive -", forgive)
					return true
				}

				return false
			},
		},
	}

	sentMessage, _ = t.SendMsg(msg, "", chatID, b)
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

	buttons.createButtons(editmsg, t.callback, func() {}, 3)
	t.bot.Send(editmsg)
}
