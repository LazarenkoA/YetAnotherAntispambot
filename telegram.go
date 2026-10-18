package app

import (
	"Antispam/AI"
	"Antispam/AI/deepseek"
	"Antispam/db"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-telegram/bot/models"
	"github.com/k0kubun/pp/v3"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"io"
	"log"
	"log/slog"
	"maps"
	"math/rand"
	"net/http"
	"net/url"
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

	tgbotapi "github.com/go-telegram/bot"
	uuid "github.com/nu7hatch/gouuid"
)

type UserInfo struct {
	ID     int64
	Name   string
	Weight int64
}

type Button struct {
	caption string
	handler func(*models.Update, *Button) bool
	timer   int

	// время начала таймера
	start time.Time
	ID    string
}

const (
	keyActiveMSG             = "keyActiveMSG"
	keyActiveRandomModerator = "keyActiveRandomModerator"
	keyUsingOneTry           = "UsingOneTry"

	// hateToxicThreshold - порог hate_percent, после которого сообщение считается токсичным
	// (бот отвечает пользователю и сообщение попадает в статистику токсичности)
	hateToxicThreshold = 85
	// hateWarnThreshold - порог, после которого токсичность пишется в лог
	hateWarnThreshold = 10
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

type IMessageAnalysis interface {
	GetMessageCharacteristics(msgText, tread string) (*AI.MessageAnalysis, error)
}

type Telega struct {
	bot               *tgbotapi.Bot
	callback          map[string]func(models.Update) bool
	hooks             map[string]func(models.Update) bool
	running           int32
	r                 IRedis
	pool              sync.Map
	lastMsg           map[string]string // для хранения последнего сообщения по пользователю
	mx                sync.RWMutex
	httpServer        *http.Server
	client            *http.Client // http-клиент с прокси (если задан PROXY_URL), используется для всех запросов к Telegram
	users             map[int64]map[int64]UserInfo
	toxicity          map[int64]map[int64]*ToxicityStat                                                                            // статистика токсичности по чатам и пользователям
	replyFn           func(msg string, imgURL string, chatID int64, buttons Buttons, parentMessageID int) (*models.Message, error) // для тестов, по умолчанию ReplyMsg
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

func allowedUpdates() []string {
	return []string{
		models.AllowedUpdateMessage,
		models.AllowedUpdateChatMember,
		models.AllowedUpdateCallbackQuery,
		models.AllowedUpdateChatJoinRequest,
		models.AllowedUpdateMyChatMember,
	}
}

func (wd *Telega) New(debug bool, certFilePath string, pollingMode bool) (<-chan models.Update, error) {
	wd.callback = map[string]func(models.Update) bool{}
	wd.hooks = map[string]func(models.Update) bool{}
	wd.users = map[int64]map[int64]UserInfo{}
	wd.replyFn = wd.ReplyMsg
	wd.logger = slog.Default().With("name", "telegram")

	var err error
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
	wd.restoreToxicityInfo()
	go wd.watchKilledUsers(time.Minute)

	updates := make(chan models.Update, 100)

	opts := []tgbotapi.Option{
		tgbotapi.WithAllowedUpdates(allowedUpdates()),
		tgbotapi.WithDefaultHandler(func(ctx context.Context, b *tgbotapi.Bot, update *models.Update) {
			if update == nil {
				return
			}
			select {
			case updates <- *update:
			case <-ctx.Done():
			}
		}),
	}
	if debug {
		opts = append(opts, tgbotapi.WithDebug())
	}

	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, errors.Wrap(err, "parse PROXY_URL error")
		}

		wd.client = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(u), // поддерживаются http, https и socks5 прокси
			},
		}
		// defaultPollTimeout из библиотеки (time.Minute), pollTimeout-1с уходит в getUpdates
		opts = append(opts, tgbotapi.WithHTTPClient(time.Minute, wd.client))

		wd.logger.Info(fmt.Sprintf("используется прокси: %s", proxyURL))
	}

	wd.bot, err = tgbotapi.New(botToken, opts...)
	if err != nil {
		return nil, err
	}
	if webhookURL == "" && !pollingMode {
		// для отладки получаем через ngrok
		if webhookURL = getngrokWebhookURL(); webhookURL == "" {
			return nil, errors.New("не удалось получить WebhookURL")
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", wd.bot.WebhookHandler())
	wd.httpServer = &http.Server{Addr: ":" + port, Handler: mux}
	go wd.httpServer.ListenAndServe()

	go wd.watchActiveRandomModerator(time.Minute)

	wd.logger.Info(fmt.Sprintf("listen port: %s, debug: %v", port, debug))

	if !pollingMode {
		whParams := &tgbotapi.SetWebhookParams{
			URL:            webhookURL,
			MaxConnections: 70,
			AllowedUpdates: allowedUpdates(),
		}

		if certFilePath != "" {
			b, err := os.ReadFile(certFilePath)
			if err != nil {
				return nil, errors.Wrap(err, "open cert error")
			}

			whParams.Certificate = &models.InputFileUpload{
				Filename: filepath.Base(certFilePath),
				Data:     bytes.NewReader(b),
			}
		}

		if _, err = wd.bot.SetWebhook(wd.ctx, whParams); err != nil {
			return nil, errors.Wrap(err, "request error")
		}

		go wd.bot.StartWebhook(wd.ctx) // вебхук

		return updates, nil
	} else {
		_, _ = wd.bot.DeleteWebhook(wd.ctx, &tgbotapi.DeleteWebhookParams{}) // при использовании полинга
		go wd.bot.Start(wd.ctx)                                              // полинг
		return updates, nil
	}
}

func (wd *Telega) SendTTLMsg(msg string, chatID int64, buttons Buttons, ttl time.Duration) (*models.Message, error) {
	m, err := wd.SendMsg(wd.ctx, msg, chatID, 0, buttons)
	if err != nil {
		return nil, err
	}

	go func() {
		time.Sleep(ttl)
		wd.DeleteMessage(chatID, m.ID)
	}()

	return m, nil
}

func (wd *Telega) SendMsg(ctx context.Context, msg string, chatID, userID int64, buttons Buttons) (*models.Message, error) {
	var ephemeral *models.EphemeralMessageParameters
	if userID != 0 {
		ephemeral = &models.EphemeralMessageParameters{
			ReceiverUserID: userID,
		}
	}

	params := &tgbotapi.SendMessageParams{
		ChatID:                     chatID,
		EphemeralMessageParameters: ephemeral,
		Text:                       msg,
		ParseMode:                  models.ParseModeHTML,
	}
	if kb := wd.createInlineKeyboard(buttons); !lo.IsNil(kb) {
		params.ReplyMarkup = kb
	}

	return wd.bot.SendMessage(ctx, params)
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

// sendPhoto отправляет сообщение картинкой (imgURL) с подписью caption
func (wd *Telega) sendPhoto(ctx context.Context, caption, imgURL string, chatID, userID int64, parentMessageID int, buttons Buttons) (*models.Message, error) {
	p, err := downloadFile(wd.httpClient(), imgURL)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(p)

	f, err := os.Open(p)
	if err != nil {
		return nil, errors.Wrap(err, "open file error")
	}
	defer f.Close()

	params := &tgbotapi.SendPhotoParams{
		ChatID:    chatID,
		Photo:     &models.InputFileUpload{Filename: filepath.Base(p), Data: f},
		Caption:   caption,
		ParseMode: models.ParseModeHTML,
	}
	if parentMessageID != 0 {
		params.ReplyParameters = &models.ReplyParameters{MessageID: parentMessageID}
	}
	if userID != 0 {
		params.EphemeralMessageParameters = &models.EphemeralMessageParameters{
			ReceiverUserID: userID,
		}
	}
	if kb := wd.createInlineKeyboard(buttons); !lo.IsNil(kb) {
		params.ReplyMarkup = kb
	}

	return wd.bot.SendPhoto(ctx, params)
}

func (wd *Telega) ReplyMsg(msg string, imgURL string, chatID int64, buttons Buttons, parrentMessageID int) (*models.Message, error) {
	if imgURL != "" {
		return wd.sendPhoto(wd.ctx, msg, imgURL, chatID, 0, parrentMessageID, buttons)
	}

	params := &tgbotapi.SendMessageParams{
		ChatID:          chatID,
		Text:            msg,
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: parrentMessageID},
	}
	if kb := wd.createInlineKeyboard(buttons); !lo.IsNil(kb) {
		params.ReplyMarkup = kb
	}

	return wd.bot.SendMessage(wd.ctx, params)
}

func (wd *Telega) SendFile(ctx context.Context, chatID int64, fpath string) error {
	f, err := os.Open(fpath)
	if err != nil {
		return errors.Wrap(err, "open file error")
	}
	defer f.Close()

	_, err = wd.bot.SendDocument(ctx, &tgbotapi.SendDocumentParams{
		ChatID: chatID,
		Document: &models.InputFileUpload{
			Filename: filepath.Base(fpath),
			Data:     f,
		},
		ParseMode: models.ParseModeHTML,
	})

	return err
}

func (wd *Telega) createInlineKeyboard(buttons Buttons) *models.InlineKeyboardMarkup {
	if len(buttons) == 0 {
		return nil
	}

	btns := make([]models.InlineKeyboardButton, 0, len(buttons))
	for i, _ := range buttons {
		currentButton := buttons[i]

		handler := currentButton.handler
		if currentButton.ID == "" {
			UUID, _ := uuid.NewV4()
			currentButton.ID = UUID.String()
		}

		wd.callback[currentButton.ID] = func(update models.Update) bool {
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

		btns = append(btns, models.InlineKeyboardButton{
			Text:         caption,
			CallbackData: currentButton.ID,
		})
	}

	return &models.InlineKeyboardMarkup{
		InlineKeyboard: breakButtonsByColum(btns, 3),
		ForceReply:     false,
	}
}

func (wd *Telega) EditMsg(msg *models.Message, txt string, buttons Buttons) *models.Message {
	params := &tgbotapi.EditMessageTextParams{
		ChatID:    msg.Chat.ID,
		MessageID: msg.ID,
		Text:      txt,
		ParseMode: models.ParseModeHTML,
	}

	if kb := wd.createInlineKeyboard(buttons); !lo.IsNil(kb) {
		params.ReplyMarkup = kb
	}
	m, _ := wd.bot.EditMessageText(wd.ctx, params)

	return m
}

func (wd *Telega) MeIsAdmin(chatID int64) bool {
	me, err := wd.bot.GetMe(wd.ctx)
	if err != nil {
		return false
	}
	return wd.UserIsAdmin(chatID, me.ID)
}

func (wd *Telega) getChatAdministrators(chatID int64) []models.ChatMember {
	admins, _ := wd.bot.GetChatAdministrators(wd.ctx, &tgbotapi.GetChatAdministratorsParams{ChatID: chatID})
	return admins
}

// chatMemberUser возвращает пользователя из объединения models.ChatMember
func chatMemberUser(m models.ChatMember) *models.User {
	switch m.Type {
	case models.ChatMemberTypeOwner:
		return m.Owner.User
	case models.ChatMemberTypeAdministrator:
		return &m.Administrator.User
	case models.ChatMemberTypeMember:
		return m.Member.User
	case models.ChatMemberTypeRestricted:
		return m.Restricted.User
	case models.ChatMemberTypeLeft:
		return m.Left.User
	case models.ChatMemberTypeBanned:
		return m.Banned.User
	}
	return nil
}

func (wd *Telega) UserIsAdmin(chatID int64, userID int64) bool {
	for _, a := range wd.getChatAdministrators(chatID) {
		if u := chatMemberUser(a); u != nil && u.ID == userID {
			return true
		}
	}

	return false
}

func (wd *Telega) AppointModerator(chatID int64, user *UserInfo, deadline time.Time) error {
	_, err := wd.bot.PromoteChatMember(wd.ctx, &tgbotapi.PromoteChatMemberParams{
		ChatID:             chatID,
		UserID:             user.ID,
		CanDeleteMessages:  true,
		CanInviteUsers:     true,
		CanRestrictMembers: true,
	})
	if err == nil {
		wd.storeNewModerator(chatID, user, deadline)
	}

	return err
}

func (wd *Telega) RemoveModerator(chatID, userID int64) error {
	_, err := wd.bot.PromoteChatMember(wd.ctx, &tgbotapi.PromoteChatMemberParams{
		ChatID: chatID,
		UserID: userID,
	})
	if err == nil {
		wd.r.Delete(keyActiveRandomModerator + strconv.FormatInt(chatID, 10))
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

func (wd *Telega) UserIsCreator(chatID int64, userID int64) bool {
	for _, a := range wd.getChatAdministrators(chatID) {
		if a.Type == models.ChatMemberTypeOwner {
			if u := chatMemberUser(a); u != nil && u.ID == userID {
				return true
			}
		}
	}

	return false
}

func (wd *Telega) setTimer(msg models.Message, buttons Buttons, cxt context.Context) {
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

func (wd *Telega) CallbackQuery(update models.Update) bool {
	if update.CallbackQuery == nil || update.CallbackQuery.Message.Message == nil {
		return false
	}

	if call, ok := wd.callback[update.CallbackQuery.Data]; ok {
		if call != nil {
			if call(update) {
				wd.DeleteMessage(update.CallbackQuery.Message.Message.Chat.ID, update.CallbackQuery.Message.Message.ID)
				delete(wd.callback, update.CallbackQuery.Data)
			}
		}
	}

	return true
}

// httpClient возвращает http-клиент: с прокси, если задан PROXY_URL, иначе дефолтный
func (wd *Telega) httpClient() *http.Client {
	if wd.client != nil {
		return wd.client
	}
	return http.DefaultClient
}

func (wd *Telega) downloadFile(filepath, url string) error {
	resp, err := wd.httpClient().Get(url)
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

func (wd *Telega) GetUser(update *models.Update) *models.User {
	if update == nil {
		return nil
	} else if update.Message != nil {
		return update.Message.From
	} else if update.CallbackQuery != nil {
		return &update.CallbackQuery.From
	} else {
		return nil
	}
}

func (wd *Telega) GetMessage(update models.Update) *models.Message {
	if update.Message != nil {
		return update.Message
	} else if update.CallbackQuery != nil && update.CallbackQuery.Message.Message != nil {
		return update.CallbackQuery.Message.Message
	} else {
		return nil
	}
}

// messageCommand возвращает команду из сообщения (без аргументов и без @botname), аналог Message.Command() из старой библиотеки
func messageCommand(m *models.Message) string {
	if m == nil || m.Text == "" || m.Text[0] != '/' {
		return ""
	}

	command := strings.SplitN(m.Text[1:], " ", 2)[0]
	if i := strings.Index(command, "@"); i >= 0 {
		command = command[:i]
	}

	return command
}

func (wd *Telega) ReadFile(message *models.Message) (data string, err error) {
	// message.Chat.ID
	downloadFilebyID := func(FileID string) {
		file, e := wd.bot.GetFile(wd.ctx, &tgbotapi.GetFileParams{FileID: FileID})
		if e != nil {
			err = e
			return
		}

		_, fileName := path.Split(file.FilePath)
		filePath := path.Join(os.TempDir(), fileName)
		defer os.Remove(filePath)

		if err = wd.downloadFile(filePath, fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", botToken, file.FilePath)); err != nil {
			return
		}
		if dataByte, e := os.ReadFile(filePath); e == nil {
			data = string(dataByte)
		} else {
			err = e
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
	wd.restrictChatMember(chatID, userID, duration, false)
}

func (wd *Telega) EnableWritingMessages(chatID int64, userID int64) {
	wd.restrictChatMember(chatID, userID, 0, true)
}

func (wd *Telega) restrictChatMember(chatID int64, userID int64, duration time.Duration, allow bool) {
	var untilDate int64
	if duration > 0 {
		untilDate = time.Now().Add(duration).Unix()
	}

	_, err := wd.bot.RestrictChatMember(wd.ctx, &tgbotapi.RestrictChatMemberParams{
		ChatID:    chatID,
		UserID:    userID,
		UntilDate: int(untilDate),
		Permissions: &models.ChatPermissions{
			CanSendMessages:       allow,
			CanSendAudios:         allow,
			CanSendDocuments:      allow,
			CanSendPhotos:         allow,
			CanSendVideos:         allow,
			CanSendVideoNotes:     allow,
			CanSendVoiceNotes:     allow,
			CanSendPolls:          allow,
			CanSendOtherMessages:  allow,
			CanAddWebPagePreviews: allow,
			CanChangeInfo:         allow,
			CanInviteUsers:        allow,
			CanPinMessages:        allow,
		},
	})
	if err != nil {
		wd.logger.Error(fmt.Sprintf("При изменении прав пользователя произошла ошибка: %v\n", err))
	}
}

func (wd *Telega) KickChatMember(chatID int64, user models.User) {
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

// CheckMessageModeration проверяет сообщение на токсичность и оффтоп
func (wd *Telega) CheckMessageModeration(msg *models.Message, conf *Conf) {
	if msg == nil || msg.From == nil || conf == nil || !conf.AI.AIEnabled() {
		return
	}

	wd.logger.Debug("message AI moderation analysis")

	getMessageCharacteristics := wd.aiClient(msg.Chat.ID, conf.AI)
	analysis, err := getMessageCharacteristics(msg.Text, wd.getThreadMessages(msg))
	if err != nil {
		// AI недоступен (ошибка API, нет денег на счету и т.п.) - статистику токсичности не трогаем,
		// что бы % не искажался в сторону уменьшения
		wd.logger.Error(errors.Wrap(err, "GetMessageCharacteristics error").Error())
		return
	}

	stat := wd.RecordToxicity(msg.Chat.ID, msg.From.ID, analysis.HatePercent)

	if analysis.HatePercent >= hateToxicThreshold {
		wd.replyFn(formatToxicReply(analysis, stat), "", msg.Chat.ID, Buttons{}, msg.ID)
	} else if analysis.HatePercent >= hateWarnThreshold {
		wd.logger.Info(fmt.Sprintf("msg: %s, HatePercent: %d, HateReason: %s", msg.Text, analysis.HatePercent, analysis.HateReason))
	}

	//if analysis.IsOffTopic {
	//	wd.ReplyMsg("Ваше сообщение не относится к тематике чата", "", msg.Chat.ID, Buttons{}, msg.MessageID)
	//}
}

// formatToxicReply формирует текст ответа на токсичное сообщение с % токсичности сообщения и пользователя
func formatToxicReply(analysis *AI.MessageAnalysis, stat ToxicityStat) string {
	return fmt.Sprintf("Ваше сообщение похоже на токсичное (токсичность сообщения: %d%%).\n"+
		"Вот почему:\n%s\n\n"+
		"📊 Токсичность ваших сообщений: %.0f%% (%d токсичных из %d проверенных)",
		analysis.HatePercent, analysis.HateReason, stat.Percent(), stat.Toxic, stat.Total)
}

func (wd *Telega) IsSPAM(userID, chatID int64, msg string, conf *Conf) (bool, string) {
	if conf == nil || !conf.AI.AIEnabled() {
		return false, ""
	}

	wd.mx.Lock()
	defer wd.mx.Unlock()

	logger := wd.logger.With("userID", userID, "chatID", chatID)

	// если есть последнее сообщение тогда выходим, не проверяем
	if _, ok := wd.lastMsg[strconv.FormatInt(userID, 10)]; ok {
		return false, ""
	}

	getMessageCharacteristics := wd.aiClient(chatID, conf.AI)
	analysis, err := getMessageCharacteristics(strings.ReplaceAll(msg, "\n", " "), "")
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

// aiClient возвращает функцию анализа сообщения. Клиенты создаются один раз на чат и кэшируются в pool.
// Если ни один клиент создать не удалось, пустой список в pool не сохраняется, что бы при следующем
// вызове попытаться создать клиентов заново
func (wd *Telega) aiClient(chatID int64, aiConf AIConf) func(msgText, tread string) (*AI.MessageAnalysis, error) {
	if clients, ok := wd.pool.Load(chatID); ok {
		return func(msgText, tread string) (*AI.MessageAnalysis, error) {
			return wd.getMessageCharacteristics(clients.([]IMessageAnalysis), msgText, tread)
		}
	}

	var clients []IMessageAnalysis
	if aiConf.AIEnabled() {
		for _, APIKey := range aiConf.Deepseek.APIKeys {
			if ds, err := deepseek.NewDSClient(wd.ctx, APIKey); err == nil {
				clients = append(clients, ds)
			} else {
				wd.logger.Error(fmt.Sprintf("deepseek client create error (key #%d): %s", len(clients)+1, err))
			}
		}
	}

	if len(clients) > 0 {
		wd.pool.Store(chatID, clients)
	}

	return func(msgText, tread string) (*AI.MessageAnalysis, error) {
		return wd.getMessageCharacteristics(clients, msgText, tread)
	}
}

// getMessageCharacteristics поочередно опрашивает клиентов, первый успешный ответ возвращается
func (wd *Telega) getMessageCharacteristics(clients []IMessageAnalysis, msgText, tread string) (*AI.MessageAnalysis, error) {
	for _, c := range clients {
		if r, err := c.GetMessageCharacteristics(msgText, tread); err == nil {
			return r, nil
		} else {
			wd.logger.Error(fmt.Sprintf("ai request error: %s", err))
		}
	}

	return nil, errors.New("failed to process AI request")
}

func (wd *Telega) deleteSpam(user *models.User, reason string, messageID int, chatID int64) {
	wd.logger.Info(fmt.Sprintf("chatID: %d, удален спам от пользователя %s\n", chatID, wd.UserString(user)))

	wd.DeleteMessage(chatID, messageID)

	usrName := wd.UserString(user)
	msg, err := wd.SendMsg(wd.ctx, fmt.Sprintf("%s, я удалил ваше сообщение, подозрение на спам. \n\n(%s)\n", usrName, reason), chatID, 0, Buttons{})
	if err != nil {
		wd.logger.Info(errors.Wrap(err, "send msg error").Error())
		return
	}

	time.Sleep(time.Second * 10)
	wd.DeleteMessage(chatID, msg.ID)
}

func breakButtonsByColum(Buttons []models.InlineKeyboardButton, countColum int) [][]models.InlineKeyboardButton {
	end := 0
	var result [][]models.InlineKeyboardButton

	for i := 1; i <= int(float64(len(Buttons)/countColum)); i++ {
		end = i * countColum
		start := (i - 1) * countColum
		if end > len(Buttons) {
			end = len(Buttons)
		}

		result = append(result, Buttons[start:end])
	}
	if len(Buttons)%countColum > 0 {
		result = append(result, Buttons[end:])
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

func downloadFile(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
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

func (wd *Telega) StartVoting(sourceMsg *models.Message, chatID int64, countVote int) {
	user := sourceMsg.ReplyToMessage.From
	msg := fmt.Sprintf("Что сделать с %s %s (@%s)?", user.FirstName, user.LastName, user.Username)

	b := Buttons{}
	var sentMessage *models.Message

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

	hBan := func(u *models.Update, currentButton *Button) bool {
		clear(u.CallbackQuery.From.ID)
		ban[u.CallbackQuery.From.ID] = fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName)
		if len(ban) >= countVote {
			wd.DeleteMessage(chatID, sourceMsg.ID)
			wd.DeleteMessage(chatID, sourceMsg.ReplyToMessage.ID)
			wd.kickChatMember(chatID, user.ID)

			wd.logger.Debug(fmt.Sprintf("Ban - %v", ban))
			return true
		}

		renderCaption(b)
		return false
	}
	hRO := func(u *models.Update, currentButton *Button) bool {
		clear(u.CallbackQuery.From.ID)
		ro[u.CallbackQuery.From.ID] = fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName)
		if len(ro) >= countVote {
			wd.DeleteMessage(chatID, sourceMsg.ID)
			wd.DeleteMessage(chatID, sourceMsg.ReplyToMessage.ID)

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
			handler: func(u *models.Update, currentButton *Button) bool {
				clear(u.CallbackQuery.From.ID)
				forgive[u.CallbackQuery.From.ID] = fmt.Sprintf("%d-%s %s", u.CallbackQuery.From.ID, u.CallbackQuery.From.FirstName, u.CallbackQuery.From.LastName)
				renderCaption(b)

				if len(forgive) >= countVote {
					_ = wd.DeleteMessage(chatID, sourceMsg.ID)

					wd.logger.Debug(fmt.Sprintf("forgive - %v", forgive))
					return true
				}

				return false
			},
		},
		{
			caption: "Я передумал",
			handler: func(update *models.Update, button *Button) bool {
				from := wd.GetUser(update)
				if from.ID != author {
					wd.AnswerCallbackQuery(update.CallbackQuery.ID, "Отменить может только автор")
					return false
				}

				wd.DeleteMessage(chatID, sourceMsg.ID)
				return true
			},
		},
	}

	if sentMessage, err = wd.SendMsg(wd.ctx, msg, chatID, 0, b); err == nil {
		author = sourceMsg.From.ID
	}
}

func (wd *Telega) kickChatMemberUntil(chatID, userID int64, untilDate int64) error {
	_, err := wd.bot.BanChatMember(wd.ctx, &tgbotapi.BanChatMemberParams{
		ChatID:    chatID,
		UserID:    userID,
		UntilDate: int(untilDate),
	})
	return err
}

func (wd *Telega) kickChatMember(chatID, userID int64) error {
	return wd.kickChatMemberUntil(chatID, userID, 0)
}

func (wd *Telega) unbanChatMember(chatID, userID int64) error {
	_, err := wd.bot.UnbanChatMember(wd.ctx, &tgbotapi.UnbanChatMemberParams{
		ChatID:       chatID,
		UserID:       userID,
		OnlyIfBanned: true,
	})
	return err
}

func (wd *Telega) DeleteMessage(chatID int64, messageID int) error {
	_, err := wd.bot.DeleteMessage(wd.ctx, &tgbotapi.DeleteMessageParams{
		ChatID:    chatID,
		MessageID: messageID,
	})
	return err
}

func (wd *Telega) DeleteEphemeralMessage(chatID, userID int64, messageID int) error {
	_, err := wd.bot.DeleteEphemeralMessage(wd.ctx, &tgbotapi.DeleteEphemeralMessageParams{
		ChatID:             chatID,
		ReceiverUserID:     userID,
		EphemeralMessageID: messageID,
	})
	return err
}

func (wd *Telega) SaveMember(chatID int64, user *models.User) {
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

func (wd *Telega) UserString(user *models.User) string {
	userName := ""
	if user.Username != "" {
		userName = "@" + user.Username
	} else {
		userName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
	}

	return userName
}

func (wd *Telega) CastUserToUserinfo(tUser *models.User) *UserInfo {
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

	if len(allUsers) == 0 {
		return
	}

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
	_, err := wd.bot.AnswerCallbackQuery(wd.ctx, &tgbotapi.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQueryID,
		Text:            txt,
		ShowAlert:       true,
	})
	return err
}

func (wd *Telega) EditButtons(msg *models.Message, buttons Buttons) {
	var err error

	if msg.Caption != "" {
		params := &tgbotapi.EditMessageCaptionParams{
			ChatID:    msg.Chat.ID,
			MessageID: msg.ID,
			Caption:   msg.Caption,
			ParseMode: models.ParseModeHTML,
		}
		if kb := wd.createInlineKeyboard(buttons); !lo.IsNil(kb) {
			params.ReplyMarkup = kb
		}

		_, err = wd.bot.EditMessageCaption(wd.ctx, params)
	} else {
		params := &tgbotapi.EditMessageTextParams{
			ChatID:    msg.Chat.ID,
			MessageID: msg.ID,
			Text:      msg.Text,
			ParseMode: models.ParseModeHTML,
		}
		if kb := wd.createInlineKeyboard(buttons); !lo.IsNil(kb) {
			params.ReplyMarkup = kb
		}

		_, err = wd.bot.EditMessageText(wd.ctx, params)
	}

	if err != nil {
		wd.logger.Error(fmt.Sprintf("edit message error: %v", err))
	}
}

func (wd *Telega) Shutdown() {
	wd.storeUsersInfo()
	wd.storeToxicityInfo()

	wd.mx.RLock()
	wd.r.SetMap(lastMsgKey, wd.lastMsg)
	wd.mx.RUnlock()

	wd.cancel() // останавливает полинг/обработку вебхуков (Start/StartWebhook следят за контекстом)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
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

func (wd *Telega) CheckAndBlockMember(chatID int64, appendedUser *models.User, conf *Conf) bool {
	if conf.BlockMembers.UserNameRegExp == "" || appendedUser == nil {
		return false
	}

	name := appendedUser.FirstName
	if appendedUser.LastName != "" {
		name += " " + appendedUser.LastName
	}

	exp := conf.BlockMembers.UserNameRegExp

	if wd.checkRegExp(wd.UserString(appendedUser), exp) || wd.checkRegExp(name, exp) {
		wd.kickChatMember(chatID, appendedUser.ID)
		wd.logger.Info(fmt.Sprintf("пользователь %q был заблокирован в соответствии с настройками \"blockMembers\"\n", wd.UserString(appendedUser)))
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
