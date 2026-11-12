package phone

import "errors"

// errNoLAN 本机没有可给手机用的私网地址。
var errNoLAN = errors.New("没有可用的局域网地址")
