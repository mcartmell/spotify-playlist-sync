package sps

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adrg/strutil"
	"github.com/adrg/strutil/metrics"
	"github.com/joho/godotenv"
)

func init() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
}

var (
	clientID, clientSecret, discogsToken string
)

const (
	spotifyAuthorizeURL = "https://accounts.spotify.com/authorize"
	discogsSearchURL    = "https://api.discogs.com/database/search"
)

type Album struct {
	Title string
}

type SpotifyAlbumResponse struct {
	Tracks struct {
		Items []struct {
			URI string `json:"uri"`
		} `json:"items"`
	} `json:"tracks"`
}

type SpotifyPlaylistResponse struct {
	Items []struct {
		Track struct {
			URI string `json:"uri"`
		} `json:"track"`
	} `json:"items"`
	Next string `json:"next"`
}

type SpotifySearchResponse struct {
	Albums struct {
		Items []struct {
			AlbumType string `json:"album_type"`
			Artists   []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Name        string `json:"name"`
			ReleaseDate string `json:"release_date"`
			Href        string `json:"href"`
		}
	}
}

type DiscogsSearchResult struct {
	Title     string `json:"title"`
	Community struct {
		Have int `json:"have"`
	} `json:"community"`
	Format    []string `json:"format"`
	Year      string   `json:"year"`
	Style     []string `json:"style"`
	Thumb     string   `json:"thumb"`
	Uri       string   `json:"uri"`
	Artist    []string `json:"artist"`
	MasterURL string   `json:"master_url"`
}

type DiscogsSearchResponse struct {
	Results    []DiscogsSearchResult `json:"results"`
	Pagination struct {
		Items   int `json:"items"`
		PerPage int `json:"per_page"`
		Page    int `json:"page"`
		Pages   int `json:"pages"`
		Urls    struct {
			Last string `json:"last"`
			Next string `json:"next"`
		} `json:"urls"`
	} `json:"pagination"`
}

type DiscogsMasterResponse struct {
	Year int `json:"year"`
}

type SpotifyPlaylistSync struct {
	accessToken    string
	excludedStyles []string
	verbose        bool
}

func NewSpotifyPlaylistSync() *SpotifyPlaylistSync {
	return &SpotifyPlaylistSync{
		accessToken: getSpotifyAccessToken(),
	}
}

func (s *SpotifyPlaylistSync) getTracksInPlaylist(playlistID string) ([]string, error) {
	var tracks []string
	playlistUrl := fmt.Sprintf("https://api.spotify.com/v1/playlists/%s/tracks?limit=100", playlistID)
	for {
		req, err := http.NewRequest("GET", playlistUrl, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Add("Authorization", "Bearer "+s.accessToken)

		body, err := doRequest(req, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var playlistResponse SpotifyPlaylistResponse
		err = json.Unmarshal(body, &playlistResponse)
		if err != nil {
			return nil, err
		}
		for _, item := range playlistResponse.Items {
			tracks = append(tracks, item.Track.URI)
		}
		if playlistResponse.Next == "" {
			break
		}
		playlistUrl = playlistResponse.Next
	}
	return tracks, nil
}

func (s *SpotifyPlaylistSync) getLatestAlbumFromBand(artist string) string {
	// lookup artist on spotify and return the name of their latest album
	// if the artist has no albums, return an empty string

	// create a url with the artist name
	searchUrl := fmt.Sprintf("https://api.spotify.com/v1/search?q=%s&type=album", url.QueryEscape("artist:"+artist))
	// create a new request
	req, err := http.NewRequest("GET", searchUrl, nil)
	if err != nil {
		log.Fatal(err)
	}
	// set authorization header
	req.Header.Add("Authorization", "Bearer "+s.accessToken)
	// make the request
	body, err := doRequest(req, http.StatusOK)
	if err != nil {
		log.Fatal(err)
	}
	// unmarshal the response into a struct
	var searchResponse SpotifySearchResponse
	err = json.Unmarshal(body, &searchResponse)
	if err != nil {
		log.Fatal(err)
	}
	// sort albums by release date and return the latest one
	sort.Slice(searchResponse.Albums.Items, func(i, j int) bool {
		return searchResponse.Albums.Items[i].ReleaseDate > searchResponse.Albums.Items[j].ReleaseDate
	})
	if len(searchResponse.Albums.Items) == 0 {
		return ""
	}
	for _, album := range searchResponse.Albums.Items {
		if areStringsSimilar(album.Artists[0].Name, artist) {
			return album.Name
		}
	}
	return searchResponse.Albums.Items[0].Name
}

func areStringsSimilar(str1 string, str2 string) bool {
	return strutil.Similarity(str1, str2, metrics.NewLevenshtein()) > 0.8
}

func (s *SpotifyPlaylistSync) addLatestAlbumFromBand(playlistID, artist string, alreadySeen map[string]bool) error {
	// get the latest album from the band
	latestAlbum := s.getLatestAlbumFromBand(artist)
	fmt.Printf("latest album from %s is %s\n", artist, latestAlbum)
	album := Album{Title: fmt.Sprintf("%s - %s", artist, latestAlbum)}
	return s.addAlbumToSpotifyPlaylist(album, playlistID, "", alreadySeen)
}

func (s *SpotifyPlaylistSync) syncSpotifyPlaylist(playlistID, genre, year string) error {
	// get current songs in playlist
	currentTracks, err := s.getTracksInPlaylist(playlistID)
	if err != nil {
		return err
	}
	fmt.Println("got", len(currentTracks), "tracks in playlist")
	tracksAlreadySeen := map[string]bool{}
	for _, track := range currentTracks {
		tracksAlreadySeen[track] = true
	}
	albumsToAdd := make(chan Album)
	// start a goroutine to process albums
	go func() {
		for album := range albumsToAdd {
			for i := 0; i < 3; i++ {
				err := s.addAlbumToSpotifyPlaylist(album, playlistID, year, tracksAlreadySeen)
				if err == nil {
					break
				}
				// retry if there was an error
				fmt.Println("retrying", album.Title)
				time.Sleep(30 * time.Second)
			}
		}
	}()
	return s.searchDiscogsForAlbums(albumsToAdd, genre, year)
}

func doRequest(req *http.Request, expectedStatusCode int) ([]byte, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	if resp.StatusCode != expectedStatusCode {
		body, err := io.ReadAll(resp.Body)
		// replace above using os package

		if err != nil {
			return nil, err
		}
		return nil, errors.New(string(body))
	}
	return io.ReadAll(resp.Body)
}

func (s *SpotifyPlaylistSync) v(f string, args ...interface{}) {
	if !s.verbose {
		return
	}
	fmt.Printf(f, args...)
}

func (s *SpotifyPlaylistSync) addAlbumToSpotifyPlaylist(album Album, playlistID, year string, currentTracks map[string]bool) error {
	// do a spotify search for this album
	searchUrl := "https://api.spotify.com/v1/search"
	data := url.Values{
		"q":    {album.Title},
		"type": {"album"},
	}
	req, err := http.NewRequest("GET", searchUrl+"?"+data.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+s.accessToken)
	body, err := doRequest(req, http.StatusOK)
	if err != nil {
		return err
	}
	var searchResponse SpotifySearchResponse
	err = json.Unmarshal(body, &searchResponse)
	if err != nil {
		return err
	}
	// return if there are no albums to add
	if len(searchResponse.Albums.Items) == 0 {
		fmt.Printf("no albums found for %s\n", album.Title)
		return nil
	}
	// get the tracks for the album
	var albumUrl string
	// split album title into artist and album name
	albumParts := strings.Split(album.Title, " - ")
	artistName := albumParts[0]
	albumName := albumParts[1]

	for _, searchAlbum := range searchResponse.Albums.Items {
		// skip if release date doesn't start with the year
		if year != "" && !strings.HasPrefix(searchAlbum.ReleaseDate, year) {
			s.v("skipping %s because release date is %s\n", searchAlbum.Name, searchAlbum.ReleaseDate)
			continue
		}
		if areStringsSimilar(searchAlbum.Artists[0].Name, artistName) && areStringsSimilar(searchAlbum.Name, albumName) {
			albumUrl = searchAlbum.Href
			break
		}
	}
	if albumUrl == "" {
		fmt.Printf("No match for %s\n", album.Title)
		return nil
	}
	fmt.Println("adding", artistName, "-", albumName)
	req, err = http.NewRequest("GET", albumUrl, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+s.accessToken)
	body, err = doRequest(req, http.StatusOK)
	if err != nil {
		return err
	}
	var albumResponse SpotifyAlbumResponse
	err = json.Unmarshal(body, &albumResponse)
	if err != nil {
		return err
	}

	// add the tracks to the playlist
	playlistUrl := fmt.Sprintf("https://api.spotify.com/v1/playlists/%s/tracks", playlistID)
	var trackUris []string
	for _, track := range albumResponse.Tracks.Items {
		// skip if track already exists in playlist
		if currentTracks[track.URI] {
			continue
		}
		trackUris = append(trackUris, track.URI)
	}
	if len(trackUris) == 0 {
		fmt.Printf("no new tracks to add for %s - %s\n", artistName, albumName)
		return nil
	}
	// encode request as JSON
	type trackUrisRequest struct {
		URIs []string `json:"uris"`
	}
	tracksJSON, err := json.Marshal(trackUrisRequest{URIs: trackUris})
	if err != nil {
		return err
	}
	req, err = http.NewRequest("POST", playlistUrl, bytes.NewBuffer(tracksJSON))
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+s.accessToken)
	req.Header.Add("Content-Type", "application/json")
	_, err = doRequest(req, http.StatusCreated)
	if err != nil {
		return err
	}
	fmt.Printf("added %d tracks to playlist for %s - %s\n", len(trackUris), artistName, albumName)
	return nil
}

func (s *SpotifyPlaylistSync) searchDiscogsForAlbums(albumsToAdd chan Album, style, year string) error {
	// search params
	albumType := "release"
	format := "Album"
	// create a url with the search params
	searchUrl := fmt.Sprintf("%s?type=%s&style=%s&format=%s&year=%s&token=%s&per_page=100", discogsSearchURL, albumType, style, format, year, discogsToken)
	albumsSeen := map[string]bool{}
	for {
		fmt.Println("fetching", searchUrl)
		// create a new request
		req, err := http.NewRequest("GET", searchUrl, nil)
		if err != nil {
			return err
		}
		// set user agent
		req.Header.Add("User-Agent", "SpotifyPlaylistSync/0.1")
		// make the request
		body, err := doRequest(req, http.StatusOK)
		time.Sleep(1 * time.Second)
		if err != nil {
			return err
		}
		// unmarshal the response into a struct
		var discogsResponse DiscogsSearchResponse
		err = json.Unmarshal(body, &discogsResponse)
		if err != nil {
			return err
		}
		// iterate over results
	RESULTS:
		for _, result := range discogsResponse.Results {
			//fmt.Printf("found %s\n", result.Title)
			if len(result.Style) == 1 && !strings.HasSuffix(result.Style[0], style) {
				s.v("skipping %s - %s because it doesn't match style %s\n", result.Artist[0], result.Title, style)
				continue
			}
			if len(result.Style) > 1 && (!strings.HasSuffix(result.Style[0], style) && !strings.HasSuffix(result.Style[1], style)) {
				s.v("skipping %s because it doesn't match style %s\n", result.Title, style)
				continue
			}

			// skip if any styles are excluded
			for _, style := range result.Style {
				for _, exc := range s.excludedStyles {
					if strings.Contains(style, exc) {
						s.v("skipping %s because it contains excluded style %s\n", result.Title, exc)
						break RESULTS
					}
				}
			}

			if result.Community.Have < 10 {
				s.v("skipping %s because it has less than 10 copies\n", result.Title)
				continue
			}
			// skip if this is a reissue or remaster
			if isReissueOrRemaster(result.Format) {
				s.v("skipping %s because it is a reissue or remaster\n", result.Title)
				continue
			}
			if result.MasterURL != "" {
				masterYear, err := getMasterReleaseYear(result.MasterURL)
				if err != nil {
					return err
				}
				// skip if master release year is not the same as the search year
				if masterYear != year {
					s.v("skipping %s because master release year %s does not match search year %s\n", result.Title, masterYear, year)
					continue
				}
			}

			rx := regexp.MustCompile(`\s\(\d+\)`)
			title := rx.ReplaceAllString(result.Title, "")
			if !albumsSeen[title] {
				// send the album to the channel
				albumsToAdd <- Album{
					Title: title,
				}
				albumsSeen[title] = true
			}
		}
		// get next url
		searchUrl = discogsResponse.Pagination.Urls.Next
		if searchUrl == "" {
			break
		}
	}
	return nil
}

func getMasterReleaseYear(masterURL string) (string, error) {
	// create a new request
	masterURLWithToken := fmt.Sprintf("%s?token=%s", masterURL, discogsToken)
	req, err := http.NewRequest("GET", masterURLWithToken, nil)
	if err != nil {
		return "", err
	}
	// set user agent
	req.Header.Add("User-Agent", "SpotifyPlaylistSync/0.1")
	// make the request
	body, err := doRequest(req, http.StatusOK)
	time.Sleep(1 * time.Second)
	if err != nil {
		return "", err
	}
	// unmarshal the response into a struct
	var masterResponse DiscogsMasterResponse
	err = json.Unmarshal(body, &masterResponse)
	if err != nil {
		return "", err
	}
	// convert year to string
	year := strconv.Itoa(masterResponse.Year)
	return year, nil
}

func isReissueOrRemaster(format []string) bool {
	for _, f := range format {
		if f == "Reissue" || f == "Remastered" {
			return true
		}
	}
	return false
}

func (s *SpotifyPlaylistSync) readBandsFromFile(file string) ([]string, error) {
	var bands []string
	f, err := os.Open(file)
	if err != nil {
		return bands, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		bands = append(bands, scanner.Text())
	}
	return bands, nil
}

func (s *SpotifyPlaylistSync) addBandsFromFileToPlaylist(playlistID string, file string) error {
	bands, err := s.readBandsFromFile(file)
	if err != nil {
		return err
	}
	// get current songs in playlist
	currentTracks, err := s.getTracksInPlaylist(playlistID)
	if err != nil {
		return err
	}
	fmt.Println("got", len(currentTracks), "tracks in playlist")
	alreadySeen := map[string]bool{}
	for _, track := range currentTracks {
		alreadySeen[track] = true
	}
	for _, band := range bands {
		// add latest album from band
		if err := s.addLatestAlbumFromBand(playlistID, band, alreadySeen); err != nil {
			log.Fatal(err)
		}
		time.Sleep(1 * time.Second)
	}
	return nil
}
func Run() {
	clientSecret = os.Getenv("SPOTIFY_CLIENT_SECRET")
	clientID = os.Getenv("SPOTIFY_CLIENT_ID")
	discogsToken = os.Getenv("DISCOGS_TOKEN")

	// create flags for playlist id, style, year and file
	playlistID := flag.String("p", "", "Spotify playlist ID")
	style := flag.String("s", "", "Discogs style")
	excludeStyles := flag.String("E", "", "Discogs styles to exclude")
	year := flag.String("y", "", "Year")
	file := flag.String("f", "", "File of bands to read from")
	flag.Parse()

	s := NewSpotifyPlaylistSync()
	s.excludedStyles = strings.Split(*excludeStyles, ",")

	// if file is set, add albums from bands in file to playlist
	if *file != "" {
		// ensure playlistID is set
		if *playlistID == "" {
			log.Fatal("playlist id must be set")
		}
		if err := s.addBandsFromFileToPlaylist(*playlistID, *file); err != nil {
			log.Fatal(err)
		}
		return
	}

	// ensure playlistID, style and year are set
	if *playlistID == "" || *style == "" || *year == "" {
		log.Fatal("playlist id, style and year must be set")
	}

	err := s.syncSpotifyPlaylist(*playlistID, *style, *year)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
