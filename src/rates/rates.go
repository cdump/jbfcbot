package rates

import (
	"encoding/json"
	"fmt"
	// "log"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

const updateInterval = 1 * time.Minute

type price struct {
	Ticker   string
	Current  float64
	Prev     float64
	Currency string
}

type Rates struct {
	nextUpdate time.Time
	values     map[string]price
}

var hclient *http.Client
var tickers [][]string

func init() {
	hclient = &http.Client{}
	tickers = [][]string{
		{"Euro", "USD", "GBP", "CNY"},
		{"Brent"},
		{"Bitcoin", "Litecoin", "Ethereum", "Bitcoin Cash"},
	}
}

func New() *Rates {
	r := Rates{}
	r.values = make(map[string]price)
	return &r
}

func updateBrent(ch chan<- price) {
	var val struct {
		Attr struct {
			LastValue      float64 `json:"last_value"`
			LastCloseValue float64 `json:"last_close_value"`
		} `json:"attr"`
	}
	resp, err := hclient.Get("http://sbcharts.investing.com/charts_xml/jschart_sideblock_8833_area.json")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&val); err != nil {
		return
	}
	ch <- price{
		Ticker:   "Brent",
		Current:  val.Attr.LastValue,
		Prev:     val.Attr.LastCloseValue,
		Currency: "$",
	}
}

func updateMOEX(ch chan<- price) {
	var val struct {
		MarketData struct {
			Data [][]float64 `json:"data"`
		} `json:"marketdata"`
	}

	tickers := []string{"CNY", "Euro", "GBP", "USD"}
	resp, err := hclient.Get("http://iss.moex.com/iss/engines/currency/markets/selt/boards/CETS/securities.json?iss.meta=off&iss.only=marketdata&securities=CNYRUB_TOM,EUR_RUB__TOM,GBPRUB_TOM,USD000UTSTOM&marketdata.columns=LAST,CHANGE")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&val); err != nil {
		return
	}

	for i, ticker := range tickers {
		ch <- price{
			Ticker:   ticker,
			Current:  val.MarketData.Data[i][0],
			Prev:     val.MarketData.Data[i][0] - val.MarketData.Data[i][1],
			Currency: "₽",
		}
	}
}

func updateCrypto(ch chan<- price) {
	resp, err := hclient.Get("https://api.bitfinex.com/v2/tickers?symbols=tBTCUSD,tLTCUSD,tETHUSD,tBCHUSD")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)

	var rawData interface{}
	err = json.Unmarshal(body, &rawData)
	if err != nil {
		return
	}

	rawArray := rawData.([]interface{})

	for i, name := range []string{"Bitcoin", "Litecoin", "Ethereum", "Bitcoin Cash"} {
		d := rawArray[i].([]interface{})
		ch <- price{
			Ticker:   name,
			Current:  d[7].(float64),
			Prev:     d[7].(float64) - d[5].(float64),
			Currency: "$",
		}
	}
}

func (rates *Rates) updateIfNeed() {
	now := time.Now()
	if now.Before(rates.nextUpdate) {
		return
	}
	rates.nextUpdate = now.Add(updateInterval)

	var wg sync.WaitGroup
	ch := make(chan price, 16)

	wg.Add(3)
	go func(ch chan<- price) {
		defer wg.Done()
		updateBrent(ch)
	}(ch)

	go func(ch chan<- price) {
		defer wg.Done()
		updateMOEX(ch)
	}(ch)

	go func(ch chan<- price) {
		defer wg.Done()
		updateCrypto(ch)
	}(ch)

	wg.Wait()
	close(ch)

	for val := range ch {
		rates.values[val.Ticker] = val
	}
}

func (rates *Rates) Get() string {
	rates.updateIfNeed()
	res := ""
	for _, group := range tickers {
		for _, ticker := range group {
			p := rates.values[ticker]
			res += fmt.Sprintf("*%s*: %.2f%s", ticker, p.Current, p.Currency)
			if p.Prev != 0 {
				change := p.Current - p.Prev
				if change >= 0 {
					res += fmt.Sprintf(" : (📈 +%.2f%s)", change, p.Currency)
				} else {
					res += fmt.Sprintf(" : (📉 %.2f%s)", change, p.Currency)
				}
			}
			res += "\n"
		}
		res += "\n"
	}
	return res
}
