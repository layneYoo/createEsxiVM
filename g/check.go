package g

import (
	"os"

	"github.com/Masterminds/glide/msg"
)

func Check(b bool, mess string, err error) {
	if !b {
		if err != nil {
			msg.Err(mess, err.Error())
		} else {
			msg.Err(mess, err)
		}
		os.Exit(1)
	}
}
