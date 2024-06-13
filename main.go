package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	var token, serverURL string

	// Define root command
	var rootCmd = &cobra.Command{
		Use:   "telegram-bot",
		Short: "Telegram Bot",
		Run: func(cmd *cobra.Command, args []string) {
			// Get token and server URL from environment variables or flags
			token = os.Getenv("TELEGRAM_BOT_TOKEN")
			serverURL = os.Getenv("TELEGRAM_SERVER_URL")

			if cmd.Flag("token").Changed {
				token, _ = cmd.Flags().GetString("token")
			}

			if cmd.Flag("server").Changed {
				serverURL, _ = cmd.Flags().GetString("server")
			}

			if token == "" {
				log.Fatal("Telegram bot token is required")
			}

			if serverURL == "" {
				log.Fatal("Telegram server URL is required")
			}

			runBot(token, serverURL)
		},
	}

	// Define flags
	rootCmd.Flags().StringP("token", "t", "", "Telegram bot token")
	rootCmd.Flags().StringP("server", "s", "https://api.telegram.org/bot%s/%s", "Telegram server URL")

	// Execute the root command
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runBot(token, serverURL string) {
	// Check if ffmpeg is available
	if !isFFmpegAvailable() {
		log.Fatal("ffmpeg is not available on this system. Please install ffmpeg to use this bot.")
	}

	bot, err := tgbotapi.NewBotAPIWithAPIEndpoint(token, serverURL)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	// Set up signal handling to catch interrupts (e.g., Ctrl+C) and terminate signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// Start a goroutine to handle the signals
	go func() {
		sig := <-sigs
		log.Printf("Received signal: %s", sig)
		logOut(bot)
		os.Exit(0)
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil { // If we got a message
			if update.Message.IsCommand() {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "To use this bot, send any audio file and it will be converted to opus format.")
				bot.Send(msg)
			} else if update.Message.Audio != nil || update.Message.Voice != nil || update.Message.Document != nil || update.Message.Video != nil || update.Message.VideoNote != nil {
				var fileID string
				if update.Message.Audio != nil {
					fileID = update.Message.Audio.FileID
				} else if update.Message.Voice != nil {
					fileID = update.Message.Voice.FileID
				} else if update.Message.Document != nil {
					fileID = update.Message.Document.FileID
				} else if update.Message.Video != nil {
					fileID = update.Message.Video.FileID
				} else if update.Message.VideoNote != nil {
					fileID = update.Message.VideoNote.FileID
				}

				fileURL, err := bot.GetFileDirectURL(fileID)
				if err != nil {
					log.Println(err)
					continue
				}

				// Download the file
				inputFileName := "input"
				outputFileName := "output.opus"
				err = downloadFile(fileURL, inputFileName)
				if err != nil {
					log.Println(err)
					continue
				}

				// Convert the file to opus
				err = convertToOpus(inputFileName, outputFileName)
				if err != nil {
					log.Println(err)
					continue
				}

				// Send the converted file back to the user as a voice message
				sendVoiceMessage(bot, update.Message.Chat.ID, outputFileName)
			} else {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "To use this bot, send any audio file and it will be converted to opus format.")
				bot.Send(msg)
			}
		}
	}
}

func downloadFile(url string, fileName string) error {
	out, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer out.Close()

	response, err := http.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	_, err = io.Copy(out, response.Body)
	return err
}

func isFFmpegAvailable() bool {
	cmd := exec.Command("ffmpeg", "-version")
	err := cmd.Run()
	return err == nil
}

func convertToOpus(inputFileName string, outputFileName string) error {
	cmd := exec.Command("ffmpeg", "-nostdin", "-y", "-i", inputFileName, "-c:a", "libopus", outputFileName)

	// Capture standard output and error
	// var out bytes.Buffer
	// var stderr bytes.Buffer
	// cmd.Stdout = &out
	// cmd.Stderr = &stderr

	ret := cmd.Run()
	// log.Println(out.String(), stderr.String())
	return ret
}

func sendVoiceMessage(bot *tgbotapi.BotAPI, chatID int64, fileName string) {
	voice := tgbotapi.NewVoice(chatID, tgbotapi.FilePath(fileName))
	_, err := bot.Send(voice)
	if err != nil {
		log.Println(err)
	}
}

func logOut(bot *tgbotapi.BotAPI) {
	if _, err := bot.Request(tgbotapi.LogOutConfig{}); err != nil {
		log.Printf("Failed to log out: %v", err)
	} else {
		log.Println("Logged out successfully.")
	}
}
