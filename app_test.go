package app

//
//func Test_processingNewMembers(t *testing.T) {
//	p := gomonkey.ApplyFunc(handlerAddNewMembers, func(wd *Telega, update tgbotapi.Update, appendedUser *tgbotapi.User, conf *Conf) {})
//	p.ApplyFunc(readConf, func(wd *Telega, chatID int64) *Conf { return new(Conf) })
//	defer p.Reset()
//
//	result := processingNewMembers(new(Telega), tgbotapi.Update{})
//	assert.False(t, result)
//
//	result = processingNewMembers(new(Telega), tgbotapi.Update{
//		ChatMember: &tgbotapi.ChatMemberUpdated{
//			NewChatMember: tgbotapi.ChatMember{Status: "left"},
//		},
//	})
//	assert.False(t, result)
//
//	result = processingNewMembers(new(Telega), tgbotapi.Update{
//		ChatMember: &tgbotapi.ChatMemberUpdated{
//			NewChatMember: tgbotapi.ChatMember{Status: "member", User: &tgbotapi.User{ID: 1}},
//		},
//	})
//	assert.True(t, result)
//
//	result = processingNewMembers(new(Telega), tgbotapi.Update{
//		ChatMember: &tgbotapi.ChatMemberUpdated{
//			NewChatMember: tgbotapi.ChatMember{Status: "member", User: &tgbotapi.User{ID: 1}},
//		},
//		Message: &tgbotapi.Message{
//			Chat: &tgbotapi.Chat{ID: 21212},
//			NewChatMembers: []tgbotapi.User{
//				{ID: 1},
//				{ID: 2},
//			},
//		},
//	})
//	assert.True(t, result)
//}
