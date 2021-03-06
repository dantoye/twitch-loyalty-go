package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	twitch "github.com/gempir/go-twitch-irc"
	_ "github.com/mattn/go-sqlite3"
)

type LoyaltyRepo interface {
	Subscribe(user string) error
	Gift(user string, from string) error
	Cheer(user string, amount int) error
	UserInfo(user string) UserInfo
	ChannelInfo() ChannelInfo
}

type ChatMonitor struct {
	LoyaltyRepo
	*twitch.Client
	channel string

	messages chan string
}

func NewChatMonitor(lp LoyaltyRepo) *ChatMonitor {
	cm := &ChatMonitor{LoyaltyRepo: lp}
	cm.messages = make(chan string, 1000)
	return cm
}

func (cm *ChatMonitor) SaySlowly() {
	lastMessage := ""
	for m := range cm.messages {
		if lastMessage == m {
			continue
		}
		lastMessage = m
		log.Println("saying", m)
		cm.Client.Say(cm.channel, m)
		time.Sleep(4 * time.Second)
	}
}

func (cm *ChatMonitor) Say(s string) {
	cm.messages <- s
}

func (cm *ChatMonitor) Monitor() error {
	token := os.Getenv("USER_OAUTH_TOKEN")
	name := os.Getenv("USER_NAME")

	if len(token) == 0 {
		return fmt.Errorf("error, USER_OAUTH_TOKEN variable empty")
	}

	if len(name) == 0 {
		return fmt.Errorf("error, USER_NAME variable empty")
	}

	client := twitch.NewClient(name, token)
	cm.Client = client
	client.OnConnect(func() { log.Println("connected!") })

	channel := os.Getenv("USER_CHANNEL")
	if len(channel) == 0 {
		return fmt.Errorf("error, USER_CHANNEL variable empty")
	}
	cm.channel = channel
	cm.Join(channel)
	cm.OnPrivateMessage(cm.NewMessage)
	go cm.SaySlowly()

	return client.Connect()
}

func (cm *ChatMonitor) Subscribe(message twitch.PrivateMessage) string {
	if err := cm.LoyaltyRepo.Subscribe(message.User.Name); err != nil {
		log.Println("err sub:", err.Error())
		return fmt.Sprintf("%s, your sub failed because `%s`", message.User.DisplayName, err.Error())
	}
	return fmt.Sprintf("Thank you %s for the sub! You can now use our emotes: SeemsGood VoHiYo 4Head GivePLZ Kappa MingLee TableHere #IfYouWant!", message.User.DisplayName)
}

func (cm *ChatMonitor) AboutMe(message twitch.PrivateMessage) string {
	info := cm.LoyaltyRepo.UserInfo(message.User.Name)
	subbed := time.Since(info.LastSub) < 1*time.Hour*24*30
	parts := make([]string, 0)
	if !subbed {
		parts = append(parts, "are not currently subscribed")
	} else {
		parts = append(parts, fmt.Sprintf("have been subscribed for %d months, most recently %s ago", info.MonthsSubbed, time.Since(info.LastSub).Round(time.Second)))
	}

	if info.GiftsGiven == 0 {
		parts = append(parts, "have given 0 gift subs to the community")
	} else {
		parts = append(parts, fmt.Sprintf("have given %d gift subs", info.GiftsGiven))
	}

	if info.SubbedFrom != nil {
		parts = append(parts, fmt.Sprintf("last received a gift sub from %s", *info.SubbedFrom))
	}

	if info.BitsCheered == 0 {
		parts = append(parts, "have not cheered")
	} else {
		parts = append(parts, fmt.Sprintf("have cheered %d bits", info.BitsCheered))
	}

	return fmt.Sprintf("%s, you: %s.", message.User.DisplayName, strings.Join(parts, "; "))
}

func (cm *ChatMonitor) GiftSub(message twitch.PrivateMessage) string {
	arg := GetArgument(0, message)
	if arg == nil {
		return "To gift sub, type !giftsub <username>"
	}
	if err := cm.LoyaltyRepo.Gift(*arg, message.User.Name); err != nil {
		log.Println("err giftsub:", err.Error())
		return fmt.Sprintf("%s, your giftsub failed because `%s`", message.User.DisplayName, err.Error())
	}
	count := cm.LoyaltyRepo.UserInfo(message.User.Name).GiftsGiven
	return fmt.Sprintf("Thank you %s for the gift sub to %s! They can now use SeemsGood VoHiYo 4Head GivePLZ Kappa MingLee TableHere! You have given %d gift subs to this channel.", message.User.DisplayName, *arg, count)
}

func (cm *ChatMonitor) Stats() string {
	ci := cm.ChannelInfo()
	return fmt.Sprintf("There are currently %d active subscribers! The community has given %d gift subs and cheered %d bits, and the top gift subber is %s", ci.ActiveSubs, ci.TotalGifts, ci.TotalCheers, ci.TopGifter)
}

var prefixes = []string{"BleedPurple", "Cheer", "PogChamp", "ShowLove", "Pride", "HeyGuys", "FrankerZ",
	"SeemsGood", "Party", "Kappa", "DansGame", "EleGiggle", "TriHard", "Kreygasm", "4Head",
	"SwiftRage", "NotLikeThis", "FailFish", "VoHiYo", "PJSalt", "MrDestructoid", "bday",
	"RIPCheer", "Shamrock"}

func (cm *ChatMonitor) CheckCheers(message twitch.PrivateMessage) string {
	total := 0
	for _, p := range strings.Split(message.Message, " ") {
		for _, prefix := range prefixes {
			if strings.HasPrefix(p, prefix) {
				log.Println("found cheer", p)
				amtStr := strings.TrimPrefix(p, prefix)
				if amt, err := strconv.Atoi(amtStr); err == nil {
					total += amt
				}
			}
		}
	}
	if total > 0 {
		return cm.doCheer(message.User, total)
	}
	return ""
}

func (cm *ChatMonitor) Cheer(message twitch.PrivateMessage) string {
	arg := GetArgument(0, message)
	if arg == nil {
		return "To cheer, type !cheer <amount>, or Cheer100"
	}

	amount, err := strconv.Atoi(*arg)
	if err != nil {
		return fmt.Sprintf("%s, you must cheer a number.", message.User.DisplayName)
	}
	return cm.doCheer(message.User, amount)
}

func (cm *ChatMonitor) doCheer(t twitch.User, amount int) string {
	if amount < 0 {
		return fmt.Sprintf("%s, stop trying to steal my bits! :(", t.DisplayName)
	}
	if amount > 1000000 {
		return fmt.Sprintf("%s, I can't allow you to be so generous! GivePLZ", t.DisplayName)
	}
	if err := cm.LoyaltyRepo.Cheer(t.Name, amount); err != nil {
		log.Println("err cheering:", err.Error())
		return fmt.Sprintf("%s, your cheer failed because `%s`", t.DisplayName, err.Error())
	}
	userInfo := cm.UserInfo(t.Name)
	info := cm.ChannelInfo()
	return fmt.Sprintf("%s, thanks for cheering %d bits, for a total of %d! The community has given %d bits, enough for a new %s!", t.DisplayName, amount, userInfo.BitsCheered, info.TotalCheers, info.Treat())
}

func (cm *ChatMonitor) NewMessage(message twitch.PrivateMessage) {
	if m := cm.CheckCheers(message); m != "" {
		cm.Say(m)
		return
	}
	cmd := GetCommand(message)
	switch cmd {
	case "giftsub":
		cm.Say(cm.GiftSub(message))
		return
	case "sub":
		cm.Say(cm.Subscribe(message))
		return
	case "me":
		cm.Say(cm.AboutMe(message))
		return
	case "cheer":
		cm.Say(cm.Cheer(message))
		return
	case "stats":
		cm.Say(cm.Stats())
		return
	}
	fmt.Println(message.User.Name, ":", message.Message)
}

func GetCommand(message twitch.PrivateMessage) string {
	return strings.TrimPrefix(strings.ToLower(strings.Split(message.Message, " ")[0]), "!")
}

func GetArgument(n int, message twitch.PrivateMessage) *string {
	parts := strings.Split(message.Message, " ")
	if n+1 >= len(parts) {
		return nil
	}
	res := strings.TrimPrefix(strings.ToLower(parts[n+1]), "@")
	return &res
}

var treats = [...]string{"teddy bear", "hot choccy", "blanket", "desk plant", "wii u", "copy of mario maker", "rune scim", "egg salad", "buzzy beetle", "mazarati", "golden kappa", "time machine"}
