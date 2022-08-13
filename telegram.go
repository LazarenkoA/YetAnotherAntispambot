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
	handler *func(*tgbotapi.Update) bool
	timer   int

	// время начала таймера
	start time.Time
	ID    string
}

const keyActiveMSG = "keyActiveMSG"

type Buttons []*Button

type Telega struct {
	bot      *tgbotapi.BotAPI
	callback map[string]func(tgbotapi.Update)
	hooks    map[string]func(tgbotapi.Update) bool
	running  int32
	r        *Redis // todo: не красиво, взаимодействие с базой надо через адаптер
}

func (this *Telega) New() (result tgbotapi.UpdatesChannel, err error) {
	this.callback = map[string]func(tgbotapi.Update){}
	this.hooks = map[string]func(tgbotapi.Update) bool{}
	this.r, _ = new(Redis).Create(redisaddr)

	this.bot, err = tgbotapi.NewBotAPIWithClient(BotToken, new(http.Client))
	// this.bot.Debug = true
	if err != nil {
		return nil, err
	}
	if WebhookURL == "" {
		WebhookURL = getngrokWebhookURL() // для отладки получаем через ngrok
		if WebhookURL == "" {
			return nil, errors.New("не удалось получить WebhookURL")
		}
	}

	_, err = this.bot.SetWebhook(tgbotapi.NewWebhook(WebhookURL))
	if err != nil {
		return nil, err
	}

	go http.ListenAndServe(":"+port, nil)
	return this.bot.ListenForWebhook("/"), nil
}

func (this *Telega) SendMsg(msg string, imgURL string, chatID int64, buttons Buttons) (tgbotapi.Message, error) {
	if imgURL != "" {
		if path, err := downloadFile(imgURL); err == nil {
			defer os.RemoveAll(path)

			newmsg := tgbotapi.NewPhotoUpload(chatID, path)
			newmsg.Caption = msg
			newmsg.ParseMode = "HTML"
			return this.createButtonsAndSend(&newmsg, buttons)
		} else {
			return tgbotapi.Message{}, err
		}
	} else {
		newmsg := tgbotapi.NewMessage(chatID, msg)
		newmsg.ParseMode = "HTML"
		return this.createButtonsAndSend(&newmsg, buttons)
	}
}

func (this *Telega) ReplyMsg(msg string, imgURL string, chatID int64, buttons Buttons, parrentMessageID int) (tgbotapi.Message, error) {
	if imgURL != "" {
		if path, err := downloadFile(imgURL); err == nil {
			defer os.RemoveAll(path)

			newmsg := tgbotapi.NewPhotoUpload(chatID, path)
			newmsg.ReplyToMessageID = parrentMessageID
			newmsg.Caption = msg
			newmsg.ParseMode = "HTML"
			return this.createButtonsAndSend(&newmsg, buttons)
		} else {
			return tgbotapi.Message{}, err
		}
	} else {
		newmsg := tgbotapi.NewMessage(chatID, msg)
		newmsg.ReplyToMessageID = parrentMessageID
		newmsg.ParseMode = "HTML"
		return this.createButtonsAndSend(&newmsg, buttons)
	}
}

func (this *Telega) SendFile(chatID int64, filepath string) error {
	msg := tgbotapi.NewDocumentUpload(chatID, filepath)
	_, err := this.bot.Send(msg)
	return err
}

func (this *Telega) createButtonsAndSend(msg tgbotapi.Chattable, buttons Buttons) (tgbotapi.Message, error) {
	_, cancel := context.WithCancel(context.Background())

	// if _, ok := msg.(tgbotapi.Fileable); ok {
	//	fmt.Println(1)
	// }

	buttons.createButtons(msg, this.callback, cancel, 3)

	timerExist := false
	for _, b := range buttons {
		if timerExist = b.timer > 0; timerExist {
			break
		}
	}

	m, err := this.bot.Send(msg)

	// Отключен таймер на кнопке т.к. при большом количествет присоединившихся пользователях не будет работать
	// if timerExist {
	// 	go this.setTimer(m, buttons, cxt, cancel) // таймер кнопки
	// }

	return m, err
}

func (this *Telega) EditMsg(msg tgbotapi.Message, txt string, buttons Buttons) tgbotapi.Message {
	editmsg := tgbotapi.NewEditMessageText(msg.Chat.ID, msg.MessageID, txt)
	editmsg.ParseMode = "HTML"
	m, _ := this.createButtonsAndSend(&editmsg, buttons)

	return m
}

func (this *Telega) MeIsAdmin(chatConfig tgbotapi.ChatConfig) bool {
	me, _ := this.bot.GetMe()
	return this.UserIsAdmin(chatConfig, &me)
}

func (this *Telega) UserIsAdmin(chatConfig tgbotapi.ChatConfig, user *tgbotapi.User) bool {
	admins, err := this.bot.GetChatAdministrators(chatConfig)
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

func (this *Telega) setTimer(msg tgbotapi.Message, buttons Buttons, cxt context.Context, cancel context.CancelFunc) {
	tick := time.NewTicker(this.getDelay())
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

			buttons.createButtons(editmsg, this.callback, cancel, 3)
			this.bot.Send(editmsg)

			if button != nil {
				if button.handler != nil {
					(*button.handler)(nil)
				}

				delete(this.callback, button.ID)
				break B
			}

			<-tick.C
		}
	}
}

func (this *Telega) CallbackQuery(update tgbotapi.Update) bool {
	if update.CallbackQuery == nil {
		return false
	}
	if call, ok := this.callback[update.CallbackQuery.Data]; ok {
		if call != nil {
			call(update)
		}
		delete(this.callback, update.CallbackQuery.Data)
	}

	return true
}

func (this *Telega) downloadFile(filepath, url string) error {
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

func (this Telega) GetUser(update *tgbotapi.Update) *tgbotapi.User {
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

func (this Telega) GetMessage(update tgbotapi.Update) *tgbotapi.Message {
	if update.Message != nil {
		return update.Message
	} else if update.CallbackQuery != nil {
		return update.CallbackQuery.Message
	} else {
		return nil
	}
}

func (this *Telega) ReadFile(message *tgbotapi.Message) (data string, err error) {
	// message.Chat.ID
	downloadFilebyID := func(FileID string) {
		var file tgbotapi.File
		if file, err = this.bot.GetFile(tgbotapi.FileConfig{FileID}); err == nil {
			_, fileName := path.Split(file.FilePath)
			filePath := path.Join(os.TempDir(), fileName)
			defer os.Remove(filePath)

			err = this.downloadFile(filePath, file.Link(BotToken))
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

func (this *Telega) DisableSendMessages(chatID int64, user *tgbotapi.User) {
	denied := false
	_, err := this.bot.RestrictChatMember(tgbotapi.RestrictChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID:             chatID,
			SuperGroupUsername: "",
			ChannelUsername:    "",
			UserID:             user.ID,
		},
		CanSendMessages: &denied,
	})
	if err != nil {
		log.Printf("При ограничении прав пользователя произошла ошибка: %v\n", err)
	}
}

func (this *Telega) EnableWritingMessages(chatID int64, user *tgbotapi.User) {
	access := true
	this.bot.RestrictChatMember(tgbotapi.RestrictChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID:             chatID,
			SuperGroupUsername: "",
			ChannelUsername:    "",
			UserID:             user.ID,
		},
		CanSendMessages:       &access,
		CanSendMediaMessages:  &access,
		CanSendOtherMessages:  &access,
		CanAddWebPagePreviews: &access,
	})
}

func (this *Telega) KickChatMember(user tgbotapi.User, config tgbotapi.KickChatMemberConfig) {
	const key = "UsingOneTry"
	go func() {
		userName := ""
		if user.UserName != "" {
			userName = "@" + user.UserName
		} else {
			userName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
		}

		users, err := this.r.Items(key)
		if this.secondAttempt(users, err, strconv.Itoa(user.ID)) {
			this.bot.KickChatMember(config)
			this.r.DeleteItems(key, strconv.Itoa(user.ID))
			return
		}

		this.r.AppendItems(key, strconv.Itoa(user.ID))
		msg, _ := this.SendMsg(fmt.Sprintf("%s, мне придется Вас удалить из чата, но у Вас будет еще одна попытка входа.", userName), "", config.ChatID, Buttons{})
		time.Sleep(time.Second * 10)
		this.bot.KickChatMember(config)
		this.bot.UnbanChatMember(tgbotapi.ChatMemberConfig{
			ChatID: config.ChatID,
			UserID: user.ID,
		})

		this.bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
			ChatID:    config.ChatID,
			MessageID: msg.MessageID})
	}()
}

func (this *Telega) secondAttempt(users []string, err error, userID string) bool {
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

func (this Buttons) createButtons(msg tgbotapi.Chattable, callback map[string]func(tgbotapi.Update), cancel context.CancelFunc, countColum int) {
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

	for _, item := range this {
		handler := item.handler
		if item.ID == "" {
			UUID, _ := uuid.NewV4()
			item.ID = UUID.String()
		}

		callback[item.ID] = func(update tgbotapi.Update) {
			if handler != nil {
				if (*handler)(&update) {
					cancel()
				}
			}
		}

		caption := item.caption
		if !item.start.IsZero() {
			second := int(item.start.Sub(time.Now()).Seconds())
			caption = fmt.Sprintf("%s (%02d:%02d:%02d)", item.caption, second/3600, (second%3600)/60, second%60)
		}

		btn := tgbotapi.NewInlineKeyboardButtonData(caption, item.ID)
		Buttons = append(Buttons, btn)
	}

	keyboard.InlineKeyboard = this.breakButtonsByColum(Buttons, countColum)
}

func (this Buttons) breakButtonsByColum(Buttons []tgbotapi.InlineKeyboardButton, countColum int) [][]tgbotapi.InlineKeyboardButton {
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

func (this *Telega) getDelay() (result time.Duration) {
	result = time.Second

	defer func() {
		recover() // что б не прервалось, result у нас уже инициализирован дефолтным значением
	}()

	if items, err := this.r.Items(keyActiveMSG); err != nil {
		return result
	} else {
		delay := len(items) / 25 // одновременных изменения не должны быть чаще 30 в сек, по этому задержку устанавливаем с учетом активных сообщений (25 эт с запасом)
		if delay > 1 {
			return time.Duration(delay)
		}
	}
	return
}
