# Gemfic

Gemfic is...

...a native Gemini version of Offprint

...a lightweight (HTML-only) frontend to Offprint.

It's written in Go with the gig framework and maintained with 💙

A hosted instance is available on...

...[Gemini](gemini://gemfic.xyz)

...[The web](https://gemfic.xyz)

## Deploy

```
# apt install golang git
$ git clone https://github.com/OffprintStudios/gemfic
$ cd gemfic
$ go get
$ openssl genrsa 2048 > host.key
$ openssl req -new -x509 -nodes -sha256 -days 365 -key host.key -out host.cert
$ go build
$ tmux
$ ./gemfic
```

HTTPS support follows by proxying Gemini, for example via Kineto.
