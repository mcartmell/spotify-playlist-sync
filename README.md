# spotify-playlist-sync

This is a simple script to populate a Spotify playlist using either a text file of band names or a [Discogs.com](https://discogs.com) API search.

# Prerequisites

You will need to have a Spotify account and a [Discogs.com](https://discogs.com) account. You will also need to create a Spotify app and a Discogs app. You will need to have the following environment variables set:

  * `SPOTIFY_CLIENT_ID`
  * `SPOTIFY_CLIENT_SECRET`
  * `DISCOGS_TOKEN`

You can add these to a `.env` file for convenience.

The Spotify app should allow http://localhost:3000 as a redirect URI.

# Installation

```
go install github.com/mcartmell/spotify-playlist-sync
```

# Usage

The script does not create playlists for you. You will need to create a playlist in Spotify and then use the playlist ID in the script.

### Populate a playlist using a Discogs search by genre and year

```
spotify-playlist-sync -p <playlist-id> -s <style> -y <year> [-E <styles to exclude>]
```

Example:

```
spotify-playlist-sync -p 123123 -s "Doom Metal" -y 2022 -E "Stoner Rock"
```

### Populate a playlist from a list of favourite bands

```
spotify-playlist-sync -p <playlist-id> -f <file>
```

Example:

```
spotify-playlist-sync -p 123123 -f fav_bands.txt
```

# License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details

# Author

[Mike Cartmell](https://mike.sg)


