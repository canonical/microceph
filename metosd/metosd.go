package main

import (
	"fmt"
)

func main() {
	var c CephConf = &Conf{}
	err := c.Load()
	if err != nil {
		fmt.Println(err)
		return
	}
	fsid, ok := c.Get("fsid")
	if ok {
		fmt.Println(fsid)
	} else {
		fmt.Println("not found")
	}
}
