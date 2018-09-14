package main

import (
	"github.com/go-telegram-bot-api/telegram-bot-api"
	log "github.com/sirupsen/logrus"
	"time"

	"fmt"
	"rates"
	"strings"
	"vote"
	// "net/http"
)

const CHAT_JBFC_MAIN = -1001046873330
const CHAT_JBFC_FLOOD = -40047914

var motd map[int64]string = map[int64]string{
	CHAT_JBFC_FLOOD: "Добро пожаловать! В этом чатике флудильня.\n\nА еще я умею курсы валют (в т.ч. в привате) - /rates",
	CHAT_JBFC_MAIN:  "Добро пожаловать! В этом чатике технические темы без флуда. И помни, что не стоит отвлекать внимание 100+ людей на вопрос, который легко найти в Google.\n\nА еще я умею курсы валют (в т.ч. в привате) - /rates",
}

const helpText = "/poll_status - статистика голосования\n/poll_start - запуск нового голосования (в ЛС у бота)\n/rates - курсы валют\n\n"

const botToken = "FIXME"

const saveFile = "./vote.json"

func main() {
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
		// TimestampFormat: time.RFC1123,
		TimestampFormat: time.Stamp,
	})

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	v := vote.New(bot, CHAT_JBFC_MAIN, saveFile)
	rates := rates.New()

	// bot.Debug = true
	log.Printf("Authorized on account %s", bot.Self.UserName)

	updates, err := bot.GetUpdatesChan(tgbotapi.UpdateConfig{Timeout: 60})
	if err != nil {
		log.Panic(err)
	}

	ch := make(chan bool)
	go func(ch chan<- bool) {
		for {
			time.Sleep(1 * time.Second)
			ch <- true
		}
	}(ch)

	for {
		select {
		case _ = <-ch:
			v.Ping()
		case update := <-updates:
			if update.CallbackQuery != nil {
				v.OnButtonClick(update.CallbackQuery)
				break
			}
			msg := update.Message
			if msg == nil {
				break
			}

			llog := log.WithFields(log.Fields{
				"uid":      msg.Chat.ID,
				"username": msg.Chat.UserName,
			})

			if msg.NewChatMembers != nil && len(*msg.NewChatMembers) > 0 {
				llog.Infof("New member in %d: %+v", msg.Chat.ID, (*msg.NewChatMembers)[0])
				if txt, ok := motd[msg.Chat.ID]; ok {
					reply := tgbotapi.NewMessage(msg.Chat.ID, txt)
					reply.ParseMode = "markdown"
					bot.Send(reply)
				}
			}

			if msg.LeftChatMember != nil {
				llog.Infof("Member left %d: %+v", msg.Chat.ID, msg.LeftChatMember)
			}

			switch cmd := msg.Command(); cmd {
			case "help":
				fallthrough
			case "start":
				llog.Info("CMD start|help")

				txt := helpText
				if msg.Chat.ID == CHAT_JBFC_MAIN {
					floodAdmins := []string{}
					m, err := bot.GetChatAdministrators(tgbotapi.ChatConfig{ChatID: CHAT_JBFC_FLOOD})
					if err == nil {
						for _, u := range m {
							floodAdmins = append(floodAdmins, u.User.UserName)
						}
					}

					txt = fmt.Sprintf("%s\nЕсли хочешь попасть во флуд-чат - напиши любому из админов: %s", helpText, strings.Join(floodAdmins, ", "))
				}

				reply := tgbotapi.NewMessage(msg.Chat.ID, txt)
				// reply.ParseMode = "markdown"
				reply.ReplyToMessageID = msg.MessageID
				bot.Send(reply)

			case "poll_start":
				llog.Info("CMD poll_start")
				v.Start(msg)
			case "poll_stop":
				llog.Info("CMD poll_stop")
				v.Stop(msg)
			case "poll_status":
				llog.Info("CMD poll_status")
				v.Status(msg)
			case "rates":
				llog.Info("CMD rates")
				reply := tgbotapi.NewMessage(msg.Chat.ID, rates.Get())
				reply.ParseMode = "markdown"
				reply.ReplyToMessageID = msg.MessageID
				bot.Send(reply)
			case "":
				llog.Infof("text: %s", msg.Text)
				v.OnMessage(msg)
			default:
				llog.Info("unknown command")
			}
		}
	}

}
