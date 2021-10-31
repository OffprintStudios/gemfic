/*
Copyright (c) 2021 Alyssa Rosenzweig
Copyright (c) 2020 Peter Vernigorov [Gig boilerplate]

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package main

import (
	"encoding/json"
	"github.com/LukeEmmet/html2gemini"
	"github.com/pitr/gig"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"text/template"
)

type SectionStats struct {
	Words int `json:"words"`
}

type Section struct {
	ID       string       `json:"_id"`
	Title    string       `json:"title"`
	Stats    SectionStats `json:"stats"`
	HTMLBody string       `json:"body"`
	Body     string
}

type UserProfile struct {
	Bio        string `json:"bio"`
	Tagline    string `json:"tagline"`
	HTTPAvatar string `json:"avatar"`
}

type Author struct {
	ID       string `json:"_id"`
	Username string `json:"screenName"`

	// Available for full user objects
	Profile UserProfile
}

type Item struct {
	ID       string    `json:"_id"`
	Author   Author    `json:"author"`
	Title    string    `json:"title"`
	Sections []Section `json:"sections"`

	ShortDesc string `json:"desc"`

	/* Offprint returns descriptions as JSON */
	HTMLBody string `json:"body"`

	/* ...but we want them in Gemtext */
	Body string
}

type ItemWrap struct {
	Content Item `json:"content"`
}

// Paginated list of blogs/works/etc
type Documents struct {
	Docs        []Item `json:"docs"`
	Page        int    `json:"page"`
	TotalPages  int    `json:"totalPages"`
	HasPrevPage bool   `json:"hasPrevPage"`
	HasNextPage bool   `json:"hasNextPage"`

	AuthorUsername string
	NextPageURL    string
	BeforePageURL  string
	DisplayType    string
	Emoji          string
	Name           string
	URLName        string
}

type SearchResults struct {
	Users []Author `json:"users"`
	Blogs []Item   `json:"blogs"`
	Works []Item   `json:"works"`

	Query string
}

// We cache all resources from the backend in memory. This reduces network
// traffic and moves data processing (namely, HTML -> Gemtext translation) out
// of the hot path. Caches are simply maps from IDs to resources.

type ItemCache map[string]Item
type SectionCache map[string]Section
type AuthorCache map[string]Author

func (story *Item) Process() (err error) {
	// TODO: cache the html2gemini context?
	html2gemini_options := html2gemini.NewOptions()
	html2gemini_ctx := html2gemini.NewTraverseContext(*html2gemini_options)

	gemtext, err := html2gemini.FromString(story.HTMLBody, *html2gemini_ctx)
	if err != nil {
		return
	}

	story.Body = gemtext
	return
}

func (section *Section) Process() (err error) {
	// TODO: cache the html2gemini context?
	html2gemini_options := html2gemini.NewOptions()
	html2gemini_ctx := html2gemini.NewTraverseContext(*html2gemini_options)
	gemtext, err := html2gemini.FromString(section.HTMLBody, *html2gemini_ctx)

	if err != nil {
		return
	}

	section.Body = gemtext
	return
}

func FetchAPI(url string) (res []byte, err error) {
	print("Fetching ")
	print(url)
	print("\n")
	resp, err := http.Get("https://offprint.net/api" + url)
	if err != nil {
		return
	}

	defer resp.Body.Close()
	return ioutil.ReadAll(resp.Body)
}

func (cache *ItemCache) Get(id string, kind string) (story Item, err error) {
	story, ok := (*cache)[id]
	if ok {
		return
	}

	b, err := FetchAPI("/content/fetch-one-published?kind=" + url.QueryEscape(kind) + "&contentId=" + id)

	if err != nil {
		return
	}

	wrap := new(ItemWrap)
	if err = json.Unmarshal(b, &wrap); err != nil {
		return
	}

	story = wrap.Content
	if err = story.Process(); err != nil {
		return
	}

	(*cache)[id] = story
	return
}

func (cache *SectionCache) Get(id string) (section Section, err error) {
	section, ok := (*cache)[id]
	if ok {
		return
	}

	b, err := FetchAPI("/sections/fetch-one-by-id?published=true&sectionId=" + url.QueryEscape(id))

	if err != nil {
		return
	}

	if err = json.Unmarshal(b, &section); err != nil {
		return
	}

	if err = section.Process(); err != nil {
		return
	}

	(*cache)[id] = section
	return
}

// Offprint returns buggy JSON, using "null" as a sentinel in some cases.
// Normalize strings so we don't print literal nulls.
func NormalizeString(s *string) {
	if *s == "null" {
		*s = ""
	}
}

func (cache *AuthorCache) Get(id string) (author Author, err error) {
	if cached, ok := (*cache)[id]; ok {
		return cached, nil
	}

	b, err := FetchAPI("/user/get-profile?pseudId=" + url.QueryEscape(id))
	if err != nil {
		return
	}

	if err = json.Unmarshal(b, &author); err != nil {
		return
	}

	NormalizeString(&author.Profile.Tagline)
	NormalizeString(&author.Profile.Bio)

	(*cache)[id] = author
	return
}

type Template struct {
	templates *template.Template
}

// Gig boilerplate
func (t *Template) Render(w io.Writer, name string, data interface{}, c gig.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

func GetTeaser(gemtext string) string {
	for i, s := range gemtext {
		if s == '\n' || s == '\r' {
			/* Replace trailing punctuation with ellipsis */
			if gemtext[i-1] == '.' || gemtext[i-1] == '!' || gemtext[i-1] == '?' {
				i = i - 1
			}

			return gemtext[0:i] + "…"
		}
	}

	/* Probably just one paragraph, return as-is */
	return gemtext
}

func HandleDocuments(c gig.Context, kinds string, name string, urlname string, emoji string, gemlogCache ItemCache) error {
	page := url.QueryEscape(c.Param("Page"))
	ID := url.QueryEscape(c.Param("ID"))
	b, err := FetchAPI("/content/fetch-all-published?filter=Default&pageNum=" + page + "&userId=" + ID + "&" + kinds)

	if err != nil {
		return c.Gemini(err.Error())
	}

	wrap := new(Documents)
	if err = json.Unmarshal(b, &wrap); err != nil {
		return c.Gemini(err.Error())
	}

	if len(wrap.Docs) > 0 {
		wrap.AuthorUsername = wrap.Docs[0].Author.Username
	}

	for i, doc := range wrap.Docs {
		doc.Process()

		/* Generate a teaser for gemlogs */
		if len(doc.ShortDesc) == 0 && len(doc.Body) > 0 {
			doc.ShortDesc = GetTeaser(doc.Body)
		}

		wrap.Docs[i] = doc

		if gemlogCache != nil {
			gemlogCache[doc.ID] = doc
		}
	}

	wrap.NextPageURL = "/user/" + c.Param("ID") + "/" + name + "/" + strconv.Itoa(wrap.Page+1)
	wrap.BeforePageURL = "/user/" + c.Param("ID") + "/" + name + "/" + strconv.Itoa(wrap.Page-1)
	wrap.Emoji = emoji
	wrap.Name = name
	wrap.URLName = urlname

	return c.Render("stories.gmi", wrap)
}

func HandleSearch(c gig.Context, query string) error {
	b, err := FetchAPI("/search/get-initial-results?query=" + url.QueryEscape(query))

	if err != nil {
		return c.Gemini(err.Error())
	}

	var results SearchResults
	if err = json.Unmarshal(b, &results); err != nil {
		return c.Gemini(err.Error())
	}

	results.Query = query
	return c.Render("search.gmi", results)
}

func main() {
	// Gig instance
	g := gig.Default()

	g.Renderer = &Template{template.Must(template.ParseGlob("views/*.gmi"))}

	storyCache := make(ItemCache)
	gemlogCache := make(ItemCache)
	sectionCache := make(SectionCache)
	authorCache := make(AuthorCache)

	// Routes
	g.Handle("/", func(c gig.Context) (err error) {
		return c.Render("home.gmi", nil)
	})

	g.Handle("/latest", func(c gig.Context) (err error) {
		b, err := FetchAPI("/browse/fetch-first-new?filter=Default")

		if err != nil {
			return c.Gemini(err.Error())
		}

		var stories []Item
		if err = json.Unmarshal(b, &stories); err != nil {
			return c.Gemini(err.Error())
		}

		// Update caches of nested data. Note the full author cache can't be updated since we lack bios here.
		for _, story := range stories {
			if err = story.Process(); err != nil {
				continue
			}

			storyCache[story.ID] = story
		}

		return c.Render("latest.gmi", stories)
	})

	g.Handle("/browse/:Page", func(c gig.Context) (err error) {
		b, err := FetchAPI("/browse/fetch-all-new?filter=Default&pageNum=" + url.QueryEscape(c.Param("Page")) + "&kind=PoetryContent&kind=ProseContent")

		if err != nil {
			return c.Gemini(err.Error())
		}

		wrap := new(Documents)
		if err = json.Unmarshal(b, &wrap); err != nil {
			return c.Gemini(err.Error())
		}

		for _, doc := range wrap.Docs {
			if err = doc.Process(); err != nil {
				continue
			}

			storyCache[doc.ID] = doc
		}

		wrap.NextPageURL = "/browse/" + strconv.Itoa(wrap.Page+1)
		wrap.BeforePageURL = "/browse/" + strconv.Itoa(wrap.Page-1)

		return c.Render("browse.gmi", wrap)
	})

	g.Handle("/story/:ID", func(c gig.Context) error {
		story, err := storyCache.Get(c.Param("ID"), "ProseContent")

		if err != nil {
			return c.Gemini(err.Error())
		}

		return c.Render("story.gmi", story)
	})

	g.Handle("/gemlog/:ID", func(c gig.Context) error {
		story, err := gemlogCache.Get(c.Param("ID"), "BlogContent")

		if err != nil {
			return c.Gemini(err.Error())
		}

		return c.Render("read.gmi", story)
	})

	g.Handle("/read/:ID", func(c gig.Context) error {
		section, err := sectionCache.Get(c.Param("ID"))

		if err != nil {
			return c.Gemini(err.Error())
		}

		return c.Render("read.gmi", section)
	})

	g.Handle("/user/:ID", func(c gig.Context) error {
		author, err := authorCache.Get(c.Param("ID"))

		if err != nil {
			return c.Gemini(err.Error())
		}

		return c.Render("user.gmi", author)
	})

	g.Handle("/user/:ID/works/:Page", func(c gig.Context) error {
		return HandleDocuments(c, "kind=ProseContent&kind=PoetryContent", "works", "story", "📕", nil)
	})

	g.Handle("/user/:ID/gemlog/:Page", func(c gig.Context) error {
		return HandleDocuments(c, "kind=BlogContent", "gemlog", "gemlog", "📰", gemlogCache)
	})

	g.Handle("/search", func(c gig.Context) error {
		if query, err := c.QueryString(); err != nil {
			return c.Gemini(err.Error())
		} else if query != "" {
			return HandleSearch(c, query)
		} else {
			return c.NoContent(gig.StatusInput, "What are you looking for?")
		}
	})

	g.Run("host.cert", "host.key")
}
