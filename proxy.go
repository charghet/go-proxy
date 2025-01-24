package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

var version = "0.2.0"

func serverRead(sc chan net.Conn, sr chan []byte) {
	serverConn := <-sc
	i := 0
	for {
		b := make([]byte, 4096)
		// fmt.Println("server read:", string(b))
		t := 60
		if i < 2 {
			t = 1
		}
		serverConn.SetReadDeadline(time.Now().Add(time.Duration(t) * time.Second))
		n, err := serverConn.Read(b)
		// fmt.Println("server read end:", serverConn.LocalAddr(), string(b), "========================")
		if err != nil {
			// fmt.Println("server read error:", serverConn.LocalAddr(), err)
			if i == 1 {
				sr <- make([]byte, 0)
			} else {
				sr <- nil
			}
			i = 0
			serverConn = <-sc
			continue
		}
		sr <- b[:n]
		i++
	}
}

func serverWrite(serverConn net.Conn, sw chan []byte) {
	for {
		b := <-sw
		if b == nil {
			// fmt.Println("server write return", serverConn.LocalAddr())
			return
		}
		// fmt.Println("server write:", string(b))
		_, err := serverConn.Write(b)
		// fmt.Println("server write end:", serverConn.LocalAddr(), string(b), "============")
		if err != nil {
			fmt.Printf("server write error: %v %v\n", serverConn.LocalAddr(), err)
			return
		}
	}
}

func handleConnection(clientConn net.Conn, remoteAddr string) {
	// fmt.Printf("%v\n", clientConn.RemoteAddr())
	defer clientConn.Close()

	// 连接远程服务器
	serverConn, err := net.Dial("tcp", remoteAddr)
	if err != nil {
		fmt.Printf("连接到远程服务器失败: %v\n", err)
		return
	}

	cr := make(chan []byte, 5)
	cw := make(chan []byte, 5)
	go func() {
		for {
			b := make([]byte, 4096)
			n, err := clientConn.Read(b)
			// fmt.Println("client read", string(b), "====================")
			// fmt.Println("sleep===========")
			// time.Sleep(5 * time.Second)
			if err != nil {
				// fmt.Printf("client read error: %v\n", err)
				cr <- nil
				return
			}
			cr <- b[:n]
		}
	}()

	go func() {
		for {
			b := <-cw
			if b == nil {
				return
			}
			// fmt.Println("client write", string(b))
			_, err := clientConn.Write(b)
			// fmt.Println("client write end", string(b), "======================")
			if err != nil {
				// fmt.Printf("client write error: %v\n", err)
				return
			}
		}
	}()

	sc := make(chan net.Conn, 5)
	sr := make(chan []byte, 5)
	sw := make(chan []byte)

	go serverWrite(serverConn, sw)
	go serverRead(sc, sr)

	sc <- serverConn
	var buff = make([][]byte, 2)
	go func() {
		i := 0
		for {
			b := <-cr
			// fmt.Println("client read:", string(b))
			if i < 2 {
				buff[i] = b
				if i == 0 {
					fmt.Println(time.Now(), clientConn.RemoteAddr(), ":\n", string(b), "\n=========================================")
				}
				// fmt.Println("buff:", string(buff), "\n===============")
			}
			sw <- b
			if b == nil {
				break
			}
			i++
		}
	}()

	write_buff := false
	i := 0
	for {
		b := <-sr
		// fmt.Println("server read:", string(b))
		if i == 1 && b != nil && len(b) == 0 { // TODO i == 0 情况
			fmt.Println(time.Now(), clientConn.RemoteAddr(), "=====tlserror=====")
			sw <- nil
			serverConn.Close()
			serverConn, err = net.Dial("tcp", remoteAddr)
			if err != nil {
				fmt.Printf("连接到远程服务器失败: %v\n", err)
				return
			}
			// time.Sleep(3 * time.Second)
			sc <- serverConn
			go serverWrite(serverConn, sw)
			sw <- buff[0]
			sw <- buff[1]
			write_buff = true
			i = 0
			continue
		}
		if write_buff {
			write_buff = false
		} else {
			// fmt.Println(string(first_sr))
			// cw <- b
			cw <- b
		}
		if b == nil {
			break
		}
		i++
	}

}

type Flag struct {
	bh string // bind host. like localhost:10170
	ph string // proxy host. like localhost:20170
	v  bool   // version
	h  bool   // help
}

func getArgs() *Flag {
	var f Flag
	flag.StringVar(&f.bh, "bh", "0.0.0.0:10171", "bind host. default 0.0.0.0:10171")
	flag.StringVar(&f.ph, "ph", "localhost:20171", "proxy host. defalut localhost:20171")
	flag.BoolVar(&f.v, "v", false, "print version.")
	flag.BoolVar(&f.h, "h", false, "show help.")

	flag.Usage = func() {
		fmt.Println("forword http proxy server and retry request to improved stability")
		fmt.Fprintf(os.Stdout, "Usage:  %s [options] [path]\n", os.Args[0])
		fmt.Printf("Version: %s\n", version)
		fmt.Println("Options:")
		flag.PrintDefaults()
	}

	flag.Parse()

	if f.v {
		fmt.Println("version:", version)
		os.Exit(0)
	}

	if f.h {
		flag.Usage()
		os.Exit(0)
	}

	return &f
}

func main() {
	f := getArgs()
	localAddr := f.bh
	remoteAddr := f.ph

	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		fmt.Printf("监听失败: %v\n", err)
		return
	}
	defer listener.Close()

	fmt.Printf("listening %s and forword to %s\n", localAddr, remoteAddr)

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			fmt.Printf("接受连接失败: %v\n", err)
			continue
		}

		go handleConnection(clientConn, remoteAddr)
	}
}
