package app

import (
	"fmt"
	"github.com/go-telegram/bot/models"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/pkg/errors"
	"sort"
	"strings"
	"time"
)

type storeMsgData struct {
	MessageID        int       `json:"messageID"`
	ReplyToMessageID int       `json:"replyToMessageID"`
	Txt              string    `json:"txt"`
	DateTime         time.Time `json:"dateTime"`
	User             string    `json:"user"`
	ChatID           int64     `json:"chatID"`
}

var (
	messages *expirable.LRU[int, []storeMsgData]
)

func init() {
	messages = expirable.NewLRU[int, []storeMsgData](100, nil, time.Hour*24)
}

func (wd *Telega) getThreadMessages(msg *models.Message) string {
	if msg == nil {
		return ""
	}

	if v, ok := messages.Get(msg.ID); ok {
		return wd.printGraph(v)
	}

	return ""
}

func (wd *Telega) printGraph(list []storeMsgData) string {
	if len(list) == 0 {
		return ""
	}

	tmp := make(map[int]storeMsgData, len(list))
	for _, v := range list {
		tmp[v.MessageID] = v
	}

	var result strings.Builder

	sort.Slice(list, func(i, j int) bool {
		return list[i].DateTime.After(list[j].DateTime)
	})

	wd.printGraphHelper(&result, tmp, list[0])
	return result.String()
}

func (wd *Telega) printGraphHelper(result *strings.Builder, list map[int]storeMsgData, msg storeMsgData) int {
	depth := 0

	parentMsg, ok := list[msg.ReplyToMessageID]
	if ok {
		depth = wd.printGraphHelper(result, list, parentMsg)
	}

	result.WriteString(fmt.Sprintf("%s%d:%s: %s", strings.Repeat("\t", depth), msg.MessageID, msg.User, msg.Txt))
	result.WriteString("\n")

	return depth + 1
}

// storeMessage сохраняет сообщение в память, нужно для получение треда сообщения
func (wd *Telega) storeMessage(msg *models.Message) {
	bdata, err := wd.toDataStore(msg)
	if err != nil {
		wd.logger.Error(err.Error())
		return
	}

	if v, ok := messages.Get(bdata.ReplyToMessageID); ok {
		messages.Add(bdata.MessageID, append(v, bdata))
	} else {
		messages.Add(bdata.MessageID, []storeMsgData{bdata})
	}
}

func (wd *Telega) toDataStore(msg *models.Message) (storeMsgData, error) {
	text := msg.Text
	if msg.Document != nil {
		text = fmt.Sprintf("<document> %s", msg.Caption)
	}
	if msg.Voice != nil {
		text = "[voice]"
	}
	if msg.Sticker != nil {
		text = "[sticker]"
	}
	if len(msg.Photo) > 0 {
		text = fmt.Sprintf("[photo] %s", msg.Caption)
	}
	if msg.Poll != nil {
		text = "[poll]"
	}
	if msg.Animation != nil {
		text = fmt.Sprintf("[gif] %s", msg.Caption)
	}

	if strings.TrimSpace(text) == "" {
		return storeMsgData{}, errors.New("data store text message is empty")
	}

	sdata := storeMsgData{
		MessageID: msg.ID,
		Txt:       text,
		DateTime:  time.Unix(int64(msg.Date), 0).UTC(),
		User:      msg.From.Username,
		ChatID:    msg.Chat.ID,
	}

	if msg.From.FirstName != "" || msg.From.LastName != "" {
		sdata.User = strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
	}

	if msg.ReplyToMessage != nil {
		sdata.ReplyToMessageID = msg.ReplyToMessage.ID
	}

	return sdata, nil
}
