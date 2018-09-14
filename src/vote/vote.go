package vote

import (
	"encoding/json"
	"fmt"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	log "github.com/sirupsen/logrus"
	"os"
	"regexp"
	"time"
)

const voteDuration = 24 * time.Hour
const createTimeout = 5 * time.Minute
const timeFormat = "Jan 2 15:04:05 MST"

type state int

const (
	stateNone state = iota
	stateWaitUsername
	stateWaitName
	stateWaitDescription
	stateReview
	stateRun
)

const (
	buttonConfirmYes = "Все верно - ✅"
	buttonConfirmNo  = "Отмена - ✖"

	buttonVoteYes = "vote_yes"
	buttonVoteNo  = "vote_no"
)

type votePerson struct {
	User   *tgbotapi.User
	Reason string
}

type Vote struct {
	bot      *tgbotapi.BotAPI
	saveFile string
	ChatId   int64

	State         state
	EndTime       time.Time
	VoteMessageId int
	Creator       *tgbotapi.User

	UserName    string
	Name        string
	Description string

	VotedYes       map[int]votePerson
	VotedNo        map[int]votePerson
	VotedNoPending map[int]votePerson
}

var markdownRe = regexp.MustCompile("([\\*_`])")

func escapeMarkdown(text string) string {
	return markdownRe.ReplaceAllString(text, `\$1`)
}

func New(bot *tgbotapi.BotAPI, chatId int64, saveFile string) *Vote {
	v := Vote{bot: bot, saveFile: saveFile}
	if saveFile == "" || v.load() == false || v.ChatId != chatId {
		v.ChatId = chatId
		v.reset()
	}
	return &v
}

func (v *Vote) reset() {
	v.State = stateNone
	v.EndTime = time.Time{}
	v.VoteMessageId = 0
	v.Creator = nil
	v.UserName = ""
	v.Name = ""
	v.Description = ""
	v.VotedYes = make(map[int]votePerson)
	v.VotedNo = make(map[int]votePerson)
	v.VotedNoPending = make(map[int]votePerson)
	v.save()
}

func (v *Vote) save() {
	fh, err := os.Create(v.saveFile)
	if err != nil {
		log.Println(err)
		return
	}
	defer fh.Close()

	enc := json.NewEncoder(fh)
	if err := enc.Encode(v); err != nil {
		log.Println(err)
	}
}

func (v *Vote) load() bool {
	fh, err := os.Open(v.saveFile)
	if err != nil {
		log.Errorf("can't open saveFile: %v", err)
		return false
	}
	defer fh.Close()

	dec := json.NewDecoder(fh)
	if err := dec.Decode(v); err != nil {
		log.Errorf("can't decode saveFile: %v", err)
		return false
	}
	log.Info("saveFile loaded")
	return true
}

func (v *Vote) Ping() {
	now := time.Now()
	if v.EndTime.IsZero() == false && now.After(v.EndTime) {
		log.Infof("timer: set state from %d to %d", v.State, stateNone)
		if v.State == stateRun {
			v.finish()
		}
		v.State = stateNone
	}
}

func (v *Vote) isUserInChat(user *tgbotapi.User) bool {
	member, err := v.bot.GetChatMember(tgbotapi.ChatConfigWithUser{ChatID: v.ChatId, UserID: user.ID})
	if err != nil {
		return false
	}
	result := member.Status == "member" || member.Status == "administrator" || member.Status == "creator"
	if result == false {
		log.Warningf("User %d (%s) not in chat!", user.ID, user.UserName)
	}
	return result
}

func (v *Vote) reply(msg *tgbotapi.Message, text string) {
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = "markdown"
	reply.ReplyToMessageID = msg.MessageID
	v.bot.Send(reply)
}

func (v *Vote) sendQuestion(chatId int64, text string) error {
	reply := tgbotapi.NewMessage(chatId, text)
	// reply.ParseMode = "markdown"
	reply.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
	_, err := v.bot.Send(reply)
	return err
}

func (v *Vote) Start(msg *tgbotapi.Message) {
	if msg.Chat.IsPrivate() == false {
		v.reply(msg, "Запуск голосования возможен только в ЛС у бота")
		return
	}

	if v.isUserInChat(msg.From) == false {
		v.reply(msg, "Ты не в чате!")
		return
	}

	switch v.State {
	case stateNone:
		v.State = stateWaitUsername
		v.EndTime = time.Now().Add(createTimeout)
		v.Creator = msg.From
		v.save()
		v.reply(msg, fmt.Sprintf("*Создание нового голосования*\n\n"+
			"0⃣ Telegram username\n"+
			"1⃣ Имя/ник\n"+
			"2⃣ Достижения\n\n"+
			"На внесение информации у тебя %.0f минут",
			createTimeout.Minutes()))
		v.sendQuestion(msg.Chat.ID, "Telegram username:")

	case stateRun:
		v.reply(msg, "Другое голосование уже запущено")

	default:
		v.reply(msg, "Другое голосование в процессе создания")
	}
}

func (v *Vote) getDescription() string {
	return fmt.Sprintf("*Telegram:* %s\n*Имя/ник:* %s\n*Достижения:* %s",
		escapeMarkdown(v.UserName),
		escapeMarkdown(v.Name),
		escapeMarkdown(v.Description),
	)
}

func (v *Vote) getVoteKeyboard() *tgbotapi.InlineKeyboardMarkup {
	textYes := fmt.Sprintf("👍 (%d) - За", len(v.VotedYes))
	textNo := fmt.Sprintf("👎 (%d + %d) - Против", len(v.VotedNo), len(v.VotedNoPending))
	return &tgbotapi.InlineKeyboardMarkup{
		InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{
			[]tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData(textYes, buttonVoteYes),
				tgbotapi.NewInlineKeyboardButtonData(textNo, buttonVoteNo),
			},
		},
	}
}

func (v *Vote) updateVoteResults() {
	v.save()
	v.bot.Send(tgbotapi.NewEditMessageReplyMarkup(v.ChatId, v.VoteMessageId, *v.getVoteKeyboard()))
}

func (v *Vote) OnMessage(msg *tgbotapi.Message) {
	llog := log.WithFields(log.Fields{
		"action":   "message",
		"uid":      msg.From.ID,
		"username": msg.From.UserName,
	})

	/* qwe */
	if v.Creator != nil && v.Creator.ID == msg.From.ID {
		switch v.State {
		case stateWaitUsername:
			v.UserName = msg.Text
			v.State = stateWaitName
			v.EndTime = time.Now().Add(createTimeout)
			v.save()
			v.sendQuestion(msg.Chat.ID, "Имя/ник:")
			llog.Infof("username: %s", v.UserName)
		case stateWaitName:
			v.Name = msg.Text
			v.State = stateWaitDescription
			v.EndTime = time.Now().Add(createTimeout)
			v.save()
			v.sendQuestion(msg.Chat.ID, "Достижения:")
			llog.Infof("name: %s", v.Name)
		case stateWaitDescription:
			v.Description = msg.Text
			v.State = stateReview
			v.EndTime = time.Now().Add(createTimeout)
			v.save()
			llog.Infof("description: %s", v.Description)

			reply := tgbotapi.NewMessage(msg.Chat.ID, "Теперь проверь все еще раз\n"+v.getDescription())
			reply.ParseMode = "markdown"
			reply.ReplyMarkup = tgbotapi.ReplyKeyboardMarkup{
				OneTimeKeyboard: true,
				ResizeKeyboard:  true,
				Keyboard: [][]tgbotapi.KeyboardButton{
					[]tgbotapi.KeyboardButton{
						tgbotapi.KeyboardButton{Text: buttonConfirmYes},
						tgbotapi.KeyboardButton{Text: buttonConfirmNo},
					},
				},
			}
			v.bot.Send(reply)

		case stateReview:
			switch msg.Text {
			case buttonConfirmYes:
				v.EndTime = time.Now().Add(voteDuration)
				voteMsg := tgbotapi.NewMessage(v.ChatId, "*Голосуем*\nдо "+v.EndTime.Format(timeFormat)+"\n\n"+v.getDescription())
				voteMsg.ParseMode = "markdown"
				voteMsg.ReplyMarkup = v.getVoteKeyboard()

				v.State = stateRun
				message, _ := v.bot.Send(voteMsg)
				v.VoteMessageId = message.MessageID
				v.save()
				llog.Info("confirmed, vote started: %+v", v)

			case buttonConfirmNo:
				llog.Info("confirm = false")
				v.reply(msg, "Отменено, можешь создать новое голосование")
				v.reset()
			}
		}
	}

	if v.State == stateRun {
		uid := msg.From.ID
		if _, ok := v.VotedNoPending[uid]; ok {
			delete(v.VotedYes, uid)
			delete(v.VotedNoPending, uid)
			v.VotedNo[uid] = votePerson{User: msg.From, Reason: msg.Text}
			llog.Infof("voted NO, reason: %s", msg.Text)
			v.reply(msg, "Голос ПРОТИВ учтен")
			v.updateVoteResults()
		}
	}

}

func (v *Vote) OnButtonClick(cq *tgbotapi.CallbackQuery) {
	llog := log.WithFields(log.Fields{
		"action":   "voteButton",
		"uid":      cq.From.ID,
		"username": cq.From.UserName,
	})

	uid := cq.From.ID

	if v.isUserInChat(cq.From) == false {
		v.bot.AnswerCallbackQuery(tgbotapi.NewCallback(cq.ID, "Ты не в чате и поэтому не можешь голосовать"))
		llog.Warning("User not in chat")
		return
	}

	switch cq.Data {
	case buttonVoteYes:
		delete(v.VotedNo, uid)
		delete(v.VotedNoPending, uid)
		v.VotedYes[uid] = votePerson{User: cq.From}
		llog.Info("voted YES")

		v.updateVoteResults()
		v.bot.AnswerCallbackQuery(tgbotapi.NewCallback(cq.ID, "👍 Голос ЗА учтен"))

	case buttonVoteNo:
		delete(v.VotedYes, uid)
		delete(v.VotedNo, uid)
		v.VotedNoPending[uid] = votePerson{User: cq.From}
		llog.Info("voted NO, waiting reason")

		err := v.sendQuestion(int64(uid), "Причина голоса против?")
		if err == nil {
			v.bot.AnswerCallbackQuery(tgbotapi.NewCallback(cq.ID, ""))
		} else {
			v.bot.AnswerCallbackQuery(tgbotapi.NewCallback(cq.ID, "⚠ Напиши причину в ЛС БОТУ"))
		}
		v.updateVoteResults()
	}
}

func (v *Vote) Stop(msg *tgbotapi.Message) {
	// v.finish()
}

func (v *Vote) finish() {
	r := v.getStatusText("Голосование окончено!")

	log.Infof("vote finished: %+v", v)

	msg := tgbotapi.NewMessage(v.ChatId, r)
	msg.ParseMode = "markdown"
	v.bot.Send(msg)

	v.reset()
}

func (v *Vote) getStatusText(title string) string {
	r := fmt.Sprintf("*%s*\nсоздал %s (%d)\nдо "+v.EndTime.Format(timeFormat)+"\n\n%s\n\n",
		title,
		v.Creator.UserName,
		v.Creator.ID,
		v.getDescription(),
	)

	r += fmt.Sprintf("👍 *За* - %d голосов:\n", len(v.VotedYes))
	for _, val := range v.VotedYes {
		r += fmt.Sprintf("%s (%d), ", escapeMarkdown(val.User.UserName), val.User.ID)
	}

	r += fmt.Sprintf("\n👎 *Против* - %d голосов:\n", len(v.VotedNo))
	for _, val := range v.VotedNo {
		r += fmt.Sprintf("- %s (%d): %s\n", escapeMarkdown(val.User.UserName), val.User.ID, escapeMarkdown(val.Reason))
	}

	if len(v.VotedNoPending) != 0 {
		r += fmt.Sprintf("👎 *Против без обоснования* - %d голосов:\n", len(v.VotedNoPending))
		for _, val := range v.VotedNoPending {
			r += fmt.Sprintf("%s (%d), ", escapeMarkdown(val.User.UserName), val.User.ID)
		}
	}
	return r
}

func (v *Vote) Status(msg *tgbotapi.Message) {
	if v.State != stateRun {
		v.reply(msg, "Нет активных голосований")
		return
	}
	if v.isUserInChat(msg.From) == false {
		v.reply(msg, "Ты не в чате!")
		return
	}
	v.reply(msg, v.getStatusText("Статистика голосования"))
}
