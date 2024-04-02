package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	s "strings"
	"time"

	"github.com/ledongthuc/pdf"
	_ "github.com/mattn/go-sqlite3"
)

const keyServerAddr = "serverAddr"

type rate struct {
	Buying  float64
	Selling float64
}

type currency struct {
	Currency string
	Rate     rate
}

type ratedb struct {
	Currency   string    `json:"currency"`
	Buying     float64   `json:"buying"`
	Selling    float64   `json:"selling"`
	Created_at time.Time `json:"created_at"`
}

func logWithFileLine(err ...any) {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println(err)
}

func newCurrency(curr string, rate rate) currency {
	return currency{Currency: curr, Rate: rate}
}

func updateUpdatedTodayFlag(hasBeenUpdatedToday *bool, val bool) {
	*hasBeenUpdatedToday = val
}

func main() {
	hasBeenUpdatedToday := false
	// connect to db
	db, err := connectToDb()
	if err != nil {
		logWithFileLine(err)
	}

	// create tables if not exists
	err = createTables(db)
	if err != nil {
		logWithFileLine(err)
	}

	t := time.NewTicker(1 * time.Hour)
	defer t.Stop()

	go func() {
		for {
			select {
			case <-t.C:
				if time.Now().Hour() == 0 {
					updateUpdatedTodayFlag(&hasBeenUpdatedToday, false)
				}

				fmt.Println("has been updated today: ", hasBeenUpdatedToday)

				// TODO: check hours between 10pm and 3PM GMT
				if !hasBeenUpdatedToday {
					getRatesFromPDF(&hasBeenUpdatedToday, db)
				}
			}
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("/getRates", func(w http.ResponseWriter, r *http.Request) {
		rate, e := getLastRateFromDB(db, "USD")
		if e != nil {
			logWithFileLine("error getting rate:", e)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(rate)
	})

	ctx := context.Background()

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
		BaseContext: func(listener net.Listener) context.Context {
			ctx = context.WithValue(ctx, keyServerAddr, listener.Addr().String())
			return ctx
		},
	}

	err = server.ListenAndServe()
	if err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("Server closed\n")
		} else if err != nil {
			fmt.Printf("error listening for server one: %s\n", err)
		}
	}

	<-ctx.Done()
}

func getRatesFromPDF(hasBeenUpdatedToday *bool, db *sql.DB) {
	currency_map := map[string]string{
		"United States Dollars": "USD",
		// "Great Britain Pound":   "GBP",
		// "Euro":                  "EUR",
	}

	currencies_list := make([]currency, 0)

	err := getPdf(hasBeenUpdatedToday)
	if err != nil {
		logWithFileLine("error saving pdf file", err)
	}

	err = readPdf("Daily_Forex_Rates.pdf", currency_map, &currencies_list)
	if err != nil {
		logWithFileLine(err)
	}

	for _, curr := range currencies_list {
		err = saveRateToDB(db, curr)
		if err != nil {
			logWithFileLine(err)
		}
	}

	updateUpdatedTodayFlag(hasBeenUpdatedToday, true)

	// convert currencies_list with created_at to json string
	json_data, err := json.Marshal(currencies_list)
	if err != nil {
		logWithFileLine("unable to convert to json:", err)
	}

	fmt.Println(string(json_data))

}

func readPdf(path string, currency map[string]string, currencies_list *[]currency) error {
	f, r, err := pdf.Open(path)
	defer func() {
		_ = f.Close()
	}()
	if err != nil {
		logWithFileLine(err)
		return err
	}
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}

		rows, _ := p.GetTextByRow()
		for _, row := range rows {
			whole_word := ""
			for _, word := range row.Content {
				whole_word += word.S
			}

			for _, pre := range currency {
				if s.Contains(whole_word, pre) {
					index := s.Index(whole_word, pre)
					// +1 to remove whitespace in front
					cut_range := index + len(pre) + 1
					rates := s.Split(whole_word[cut_range:], " ")

					buying, _ := strconv.ParseFloat(rates[0], 64)
					selling, _ := strconv.ParseFloat(rates[1], 64)

					curr := newCurrency(pre, rate{buying, selling})

					*currencies_list = append(*currencies_list, curr)

				}
			}
		}
	}
	return nil
}

func getPdf(hasBeenUpdatedToday *bool) (error error) {
	resp, err := http.Get("https://www.stanbicbank.com.gh/static_file/ghana/Downloadable%20Files/Rates/Daily_Forex_Rates.pdf")
	if err != nil {
		logWithFileLine(err)
		return err
	}

	last_modified := resp.Header.Get("Last-Modified")

	// parse last modified date
	last_modified_time, _ := time.Parse(time.RFC1123, last_modified)

	res := DateEqual(time.Now(), last_modified_time)

	defer resp.Body.Close()

	if res {
		fmt.Println("New data")

		if *hasBeenUpdatedToday {
			fmt.Println("Already updated today")
			return nil
		}
	}

	file, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		logWithFileLine(readErr)
		return readErr
	}

	writeErr := os.WriteFile("Daily_Forex_Rates.pdf", file, 0644)
	if writeErr != nil {
		logWithFileLine(writeErr)
		return writeErr
	}

	return nil
}

func connectToDb() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "./finance.db")
	if err != nil {
		logWithFileLine(err)
		return nil, err
	}
	return db, nil
}

func createTables(db *sql.DB) error {
	statement, err := db.Prepare("CREATE TABLE IF NOT EXISTS rates (id INTEGER PRIMARY KEY, currency TEXT, buying FLOAT, selling FLOAT, created_at DATETIME, updated_at DATETIME)")
	if err != nil {
		logWithFileLine(err)
		return err
	}
	statement.Exec()

	statement, err = db.Prepare("CREATE TABLE IF NOT EXISTS newsletter (id INTEGER PRIMARY KEY, email TEXT, created_at TEXT, updated_at TEXT)")
	if err != nil {
		logWithFileLine(err)
		return err
	}
	statement.Exec()
	return nil
}

func getLastRateFromDB(db *sql.DB, curr string) (ratedb, error) {
	var buying float64
	var selling float64
	var created_at time.Time
	var currency string
	err := db.QueryRow("SELECT currency, buying, selling, created_at FROM rates WHERE currency = ? ORDER BY id DESC LIMIT 1", curr).Scan(&currency, &buying, &selling, &created_at)
	if err != nil {
		logWithFileLine(err)
		return ratedb{}, err
	}
	return ratedb{
		Currency:   currency,
		Buying:     buying,
		Selling:    selling,
		Created_at: created_at,
	}, nil
}

func saveRateToDB(db *sql.DB, curr currency) error {
	statement, err := db.Prepare("INSERT INTO rates (currency, buying, selling, created_at, updated_at) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		logWithFileLine(err)
		return err
	}

	t := time.Now()

	_, err = statement.Exec(curr.Currency, curr.Rate.Buying, curr.Rate.Selling, t, t)
	if err != nil {
		logWithFileLine(err)
		return err
	}
	return nil
}

func DateEqual(date1, date2 time.Time) bool {
	y1, m1, d1 := date1.Date()
	y2, m2, d2 := date2.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}
