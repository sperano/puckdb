package main

import (
	"net/http"

	"github.com/PuerkitoBio/goquery"
	"github.com/ericsperano/yfh/cmd"
	"github.com/geziyor/geziyor"
	"github.com/geziyor/geziyor/client"
	"gorm.io/gorm"
)

type Product struct {
	gorm.Model
	Code  string
	Price uint
}

func GetHTTPClient() (*http.Client, error) {
	return nil, nil
	/*
		//resp, err := client.Get("https://fantasysports.yahooapis.com/fantasy/v2/game/nhl")
		//resp, err := client.Get("https://fantasysports.yahooapis.com/fantasy/v2/league/411.l.1005")
		resp, err := client.Get("https://fantasysports.yahooapis.com/fantasy/v2/team/411.l.1005.t.7")
		if err != nil {
			log.Fatalln(err)
		}
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatalln(err)
		}
		//Convert the body to type string
		sb := string(body)
		fmt.Printf("\n%s\n", sb)
	*/
}

func main() {
	cmd.Execute()
	/*
		client, err := GetHTTPClient()
		if err != nil {
			log.Fatal(err)
		}
		_ = client
	*/
	/*

		fsman, err := fs.NewFSManager(&config)
		if err != nil {
			log.Fatal(err)
		}
		//fmt.Printf("fsman=%+v\n", fsman)
		//	data, err := fsman.DownloadTeamForDate(7, 2021, 10, 28)
		if err = fsman.Ensure(); err != nil {
			log.Fatal(err)
		}
		//fmt.Printf("Downloaded in: %+v\n", data.Path(&config))

		db, err := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
		if err != nil {
			panic("failed to connect database")
		}

		// Migrate the schema
		db.AutoMigrate(&Product{})

		/*
				def yahoo_login(username=nil, password=nil)
			    page = http_agent.get 'https://login.yahoo.com/config/login?'
			    form = page.forms.first
			    form.login = POOL_CONFIG.yahoo_username
			    form.passwd  = POOL_CONFIG.yahoo_password
			    page = http_agent.submit form
			    @logged_in_yahoo = page.forms.size == 0
			    raise YahooLoginFailed.new(POOL_CONFIG.yahoo_username, POOL_CONFIG.yahoo_password) unless @logged_in_yahoo
			    true
		*

		url := "https://hockey.fantasysports.yahoo.com/hockey/22030/7/team?pspid=782206472&activity=myteam&date=2021-11-02&stat1=S&stat2=D"
		//url = "http://quotes.toscrape.com/"
		geziyor.NewGeziyor(&geziyor.Options{
			StartURLs: []string{url},
			ParseFunc: quotesParse2,
			Exporters: []export.Exporter{&export.JSON{FileName: "/dev/stdout"}},
		}).Start()

		// Create
		db.Create(&Product{Code: "D42", Price: 100})

		// Read
		var product Product
		db.First(&product, 1)                 // find product with integer primary key
		db.First(&product, "code = ?", "D42") // find product with code D42

		// Update - update product's price to 200
		db.Model(&product).Update("Price", 200)
		// Update - update multiple fields
		db.Model(&product).Updates(Product{Price: 200, Code: "F42"}) // non-zero fields
		db.Model(&product).Updates(map[string]interface{}{"Price": 200, "Code": "F42"})

		// Delete - delete product
		db.Delete(&product, 1)
	*/
}

func quotesParse2(g *geziyor.Geziyor, r *client.Response) {
	r.HTMLDoc.Find("div.quote").Each(func(i int, s *goquery.Selection) {
		g.Exports <- map[string]interface{}{
			"text":   s.Find("span.text").Text(),
			"author": s.Find("small.author").Text(),
		}
	})
	if href, ok := r.HTMLDoc.Find("li.next > a").Attr("href"); ok {
		g.Get(r.JoinURL(href), quotesParse)
	}
}

func quotesParse(g *geziyor.Geziyor, r *client.Response) {
	r.HTMLDoc.Find("div.quote").Each(func(i int, s *goquery.Selection) {
		g.Exports <- map[string]interface{}{
			"text":   s.Find("span.text").Text(),
			"author": s.Find("small.author").Text(),
		}
	})
	if href, ok := r.HTMLDoc.Find("li.next > a").Attr("href"); ok {
		g.Get(r.JoinURL(href), quotesParse)
	}
}
