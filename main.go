package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/diamondburned/arikawa/bot/extras/infer"
	"github.com/diamondburned/arikawa/discord"
	"github.com/diamondburned/arikawa/gateway"
	"github.com/diamondburned/arikawa/state"
	"github.com/diamondburned/arikawa/utils/wsutil"
)

type Logger struct {
	path  string
	s     *state.State
	files map[discord.GuildID]*logFile
}

func NewLogger(s *state.State, path string) *Logger {
	return &Logger{path: path, s: s}
}

func (l *Logger) Close() {
	for _, file := range l.files {
		file.Sync()
		file.Close()
	}
}

type logFile struct {
	*os.File
	Year int
	Week int
}

func (l *Logger) appendEntry(gid discord.GuildID, etype EntryType, data interface{}) error {
	now := time.Now()
	year, week := now.ISOWeek()
	logfile, ok := l.files[gid]
	if logfile != nil {
		if logfile.Year != year || logfile.Week != week {
			ok = false
		}
	}
	if !ok {
		if logfile != nil {
			logfile.Sync()
			logfile.Close()
		}
		name := l.logfileName(uint64(gid), now)
		err := os.MkdirAll(filepath.Dir(name), 0700)
		if err != nil {
			return fmt.Errorf("error creating log directory: %w", err)
		}
		file, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return fmt.Errorf("error opening log file: %w")
		}
		logfile = &logFile{
			file, year, week,
		}
	}
	entry := Entry{
		Type: etype,
		Time: now,
	}
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("Logger.appendEntry: failed to Marshal data: %w", err)
	}
	entry.Data = json.RawMessage(b)
	enc := json.NewEncoder(logfile)
	enc.Encode(entry)
	return nil
}

func (l *Logger) logfileName(id uint64, t time.Time) string {
	year, week := t.ISOWeek()
	return filepath.Join(l.path, fmt.Sprintf("%d-%d/%d.ndjson", year, week, id))
}

func (l *Logger) HandleEvent(e interface{}) {
	switch e := e.(type) {
	case *gateway.MessageCreateEvent:
		l.logMessageCreateEvent(e)
	}
}

func (l *Logger) logMessageCreateEvent(m *gateway.MessageCreateEvent) {
	entry := MessageEntry{
		Author:          toUser(m.Author),
		ID:              m.ID,
		Channel:         l.toChannel(m.ChannelID),
		Content:         m.Content,
		Timestamp:       m.Timestamp,
		EditedTimestamp: m.EditedTimestamp,
	}
	err := l.appendEntry(m.GuildID, EntryMessage, entry)
	if err != nil {
		log.Println("error while logging MessageCreateEvent:", err)
	}
}

func toUser(user discord.User) User {
	return User{
		ID:  user.ID,
		Tag: fmt.Sprintf("%s#%s", user.Username, user.Discriminator),
	}
}

func (l *Logger) toChannel(cid discord.ChannelID) Channel {
	channel := Channel{ID: cid}
	ch, err := l.s.Channel(cid)
	if err == nil {
		channel.Name = ch.Name
	}
	return channel
}

type EntryType string

const (
	EntryMessage       EntryType = "msg"
	EntryMessageDelete EntryType = "delmsg"
	EntryChannel       EntryType = "chan"
)

type Entry struct {
	Type EntryType       `json:"type"`
	Time time.Time       `json:"time"`
	Data json.RawMessage `json:"data"`
}

type MessageEntry struct {
	Author          User              `json:"author"`
	ID              discord.MessageID `json:"id"`
	Channel         Channel           `json:"channel"`
	Content         string            `json:"content"`
	Timestamp       discord.Timestamp `json:"time"`
	EditedTimestamp discord.Timestamp `json:"editedTimestamp"`
}

type MessageDeleteEntry discord.MessageID

type ChannelEntry struct {
	ID    discord.ChannelID `json:"author"`
	Name  string            `json:"name"`
	Topic string            `json:"topic"`
}

type User struct {
	ID  discord.UserID `json:"id"`
	Tag string         `json:"tag"`
}

type Channel struct {
	ID   discord.ChannelID `json:"id"`
	Name string            `json:"name"`
}

func main() {
	wsutil.WSDebug = log.Println
	var token = os.Getenv("TOKEN")
	if token == "" {
		log.Fatalln("No $TOKEN given.")
	}
	s, err := state.New(token)
	logger := NewLogger(s, "dislog")
	if err != nil {
		log.Fatalln("Session failed:", err)
	}
	eventChan, _ := s.ChanFor(
		func(ev interface{}) bool {
			gid := infer.GuildID(ev)
			/*
				for _, g := range serversToLog {
					if gid == g {
						println("THIS IS DANGAN")
						return true
					}
				}
			*/
			if gid.IsValid() {
				return true
			}
			return false
		})

	if err := s.Open(); err != nil {
		log.Fatalln("Failed to connect:", err)
	}
	defer s.Close()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	for {
		select {
		case e := <-eventChan:
			logger.HandleEvent(e)
		case <-sigs:
			logger.Close()
			os.Exit(0)
		}
	}
}
