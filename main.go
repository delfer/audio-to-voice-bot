package main

import (
	"bytes"
	"errors"
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
		log.Println("Error loading .env file")
	}

	var token, serverURL, debug string

	// Define root command
	var rootCmd = &cobra.Command{
		Use:   "telegram-bot",
		Short: "Telegram Bot",
		Run: func(cmd *cobra.Command, args []string) {
			// Get token and server URL from environment variables or flags
			token = os.Getenv("TELEGRAM_BOT_TOKEN")
			serverURL = os.Getenv("TELEGRAM_SERVER_URL")
			debug = os.Getenv("DEBUG")

			if cmd.Flag("token").Changed {
				token, _ = cmd.Flags().GetString("token")
			}

			if cmd.Flag("server").Changed {
				serverURL, _ = cmd.Flags().GetString("server")
			}

			if cmd.Flag("debug").Changed {
				debug, _ = cmd.Flags().GetString("debug")
			}

			if token == "" {
				log.Fatal("Telegram bot token is required")
			}

			if serverURL == "" {
				log.Fatal("Telegram server URL is required")
			}

			runBot(token, serverURL, debug)
		},
	}

	// Define flags
	rootCmd.Flags().StringP("token", "t", "", "Telegram bot token")
	rootCmd.Flags().StringP("server", "s", "https://api.telegram.org/bot%s/%s", "Telegram server URL")
	rootCmd.Flags().StringP("debug", "d", "", "Enable debug")

	// Execute the root command
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runBot(token, serverURL, debug string) {
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
		if update.Message != nil {
			go handleUpdate(bot, update, debug)
		}
	}
}

func handleUpdate(bot *tgbotapi.BotAPI, update tgbotapi.Update, debug string) {
	chatID := update.Message.Chat.ID

	if update.Message.IsCommand() {
		msg := tgbotapi.NewMessage(chatID, "To use this bot, send any audio file and it will be converted to opus format.")
		bot.Send(msg)
		return
	}

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

	if fileID == "" {
		msg := tgbotapi.NewMessage(chatID, "To use this bot, send any audio file and it will be converted to opus format.")
		bot.Send(msg)
		return
	}

	// Оповещение о получении запроса и начале загрузки
	msg := tgbotapi.NewMessage(chatID, "Request received, starting download...")
	bot.Send(msg)

	filePath, err := GetFilePath(bot, fileID)
	if err != nil {
		log.Println(err)
		return
	}

	// Создание уникальных имен файлов
	inputFileName := generateUniqueFileName("input", chatID)
	outputFileName := generateUniqueFileName("output", chatID) + ".opus"

	if len(debug) > 0 {
		log.Println("fileID", fileID)
		log.Println("filePath", filePath)
		log.Println("FileExists", FileExists(filePath))
		log.Println("inputFileName", inputFileName)
		log.Println("outputFileName", outputFileName)
	}

	//Проверка если файл на локальном сервере уже сущетсвует
	if FileExists(filePath) {
		inputFileName = filePath
	} else {
		// Скачивание файла
		fileURL, err := bot.GetFileDirectURL(fileID)
		if err != nil {
			log.Println(err)
			return
		}
		err = downloadFile(fileURL, inputFileName, debug)
		if err != nil {
			log.Println(err)
			return
		}
	}

	// Оповещение о завершении загрузки и начале конвертирования
	msg = tgbotapi.NewMessage(chatID, "Download complete, starting conversion...")
	bot.Send(msg)

	// Конвертирование файла в opus
	err = convertToOpus(inputFileName, outputFileName, debug)
	if err != nil {
		log.Println(err)
		return
	}

	// Отправка сконвертированного файла обратно пользователю в виде голосового сообщения
	sendVoiceMessage(bot, chatID, outputFileName)

	// Удаление созданных файлов
	if len(debug) == 0 {
		cleanupFiles(inputFileName, outputFileName)
	}
}

func generateUniqueFileName(base string, chatID int64) string {
	return fmt.Sprintf("%s_%d", base, chatID)
}

func downloadFile(url string, fileName string, debug string) error {
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

	var written int64
	written, err = io.Copy(out, response.Body)
	if len(debug) > 0 {
		log.Println("File url", url)
		log.Println("fileName", fileName)
		log.Println("file written", written)
	}
	return err
}

func isFFmpegAvailable() bool {
	cmd := exec.Command("ffmpeg", "-version")
	err := cmd.Run()
	return err == nil
}

func convertToOpus(inputFileName string, outputFileName string, debug string) error {
	cmd := exec.Command("ffmpeg", "-nostdin", "-y", "-i", inputFileName, "-c:a", "libopus", outputFileName)

	// Capture standard output and error
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	ret := cmd.Run()
	if len(debug) > 0 {
		log.Println(out.String(), stderr.String())
	}
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

func cleanupFiles(files ...string) {
	for _, file := range files {
		err := os.Remove(file)
		if err != nil {
			log.Printf("Failed to remove file %s: %v", file, err)
		}
	}
}

func GetFilePath(bot *tgbotapi.BotAPI, fileID string) (string, error) {
	file, err := bot.GetFile(tgbotapi.FileConfig{fileID})

	if err != nil {
		log.Println("GetFilePath err", file)
		return "", err
	}

	return file.FilePath, nil
}

func FileExists(name string) bool {
	_, err := os.Stat(name)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	return false
}
