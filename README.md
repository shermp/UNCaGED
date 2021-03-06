# UNCaGED - Universal Networked Calibre Go Ereader Device
A library to connect to Calibre over a network, using Calibre's "Smart Devices" API

NOTE: UNCaGED is currently ALPHA software. It should be ok to use, but the API is currently not stable.

## Usage
UNCaGED is not designed to be run standalone. Rather, it should be integrated with a device specific program to provide a complete "wireless device" solution to connect with Calibre.

You can obtain UNCaGED with
```go get github.com/shermp/UNCaGED/...```

Import UNCaGED as
```
import (
    "github.com/shermp/UNCaGED/uc"
)
```

See `uncaged-cli` for example usage of the library.

Also see https://github.com/shermp/Kobo-UNCaGED for another, more elaborate example of useage.

## License
UNCaGED is licensed under the GPL3 licensing terms.

Please refer to the LICENSE file in the UNCaGED directory for further information