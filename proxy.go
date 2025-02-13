package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

var version = "0.3.0"

func handleConnection(clientConn net.Conn, remoteAddr string, f *Flag) {
	defer fmt.Println("handleConnection end")
	defer func() {
		if r := recover(); r != nil {
			debug.PrintStack()
		}
	}()

	defer clientConn.Close()

	serverConn, err := net.Dial("tcp", remoteAddr)
	if err != nil {
		fmt.Printf("连接到远程服务器失败: %v\n", err)
		return
	}

	down := make(chan interface{}, 5)
	defer func() {
		down <- nil
	}()
	cr := make(chan []byte, 5)
	cw := make(chan []byte, 5)

	// client read
	go func() {
		defer func() {
			down <- nil
			cr <- nil
			fmt.Println("client read end")
		}()
		for {
			b := make([]byte, 4096)
			n, err := clientConn.Read(b)
			// fmt.Println("client read", string(b), "====================")
			if err != nil {
				fmt.Printf("client read error: %v\n", err)
				return
			}
			cr <- b[:n]
		}
	}()

	// client write
	go func() {
		defer func() {
			fmt.Println("client write end")
		}()
		for {
			select {
			case b := <-cw:
				// fmt.Println("client write", string(b))
				_, err := clientConn.Write(b)
				if err != nil {
					fmt.Printf("client write error: %v\n", err)
					return
				}
			case <-down:
				down <- nil
				return
			}
		}
	}()

	sr := make(chan []byte, 5)
	sw := make(chan []byte)
	serverDown := make(chan interface{}, 5)

	go serverRead(serverConn, sr, down, serverDown)
	go serverWrite(serverConn, sw, down, serverDown)

	// client read to server write
	var cb bytes.Buffer
	go func() {
		i := 0
		wcb := true
		for {
			b := <-cr
			// fmt.Println("client read:", len(b), "\n====================\n", string(b), "\n====================")
			if wcb {
				cb.Write(b)
			}
			if strings.Contains(string(b), "\r\n\r\n") {
				wcb = false
			}
			sw <- b
			if b == nil {
				break
			}
			if i < 10 {
				i++
			}
		}
	}()

	// server read to client write
	i := 0
	t := 0
	var cbr *bytes.Reader = nil
	http := true
	var sb strings.Builder
	wsb := true
	write_buff := false
	write_head := false
	for {
		b := <-sr
		if b == nil {
			break
		}
		// fmt.Println("server read:", len(b), "\n====================\n", string(b), b, "\n====================")
		if wsb {
			sb.Write(b)
		}
		if strings.Contains(sb.String(), "\r\n\r\n") {
			wsb = false
		}
		if i == 0 && strings.HasPrefix(string(b), "CONNECT") {
			http = false
		}
		if !write_head && http && !wsb {
			if strings.HasPrefix(sb.String(), "HTTP/1.1 503 Service Unavailable") && strings.Contains(sb.String(), "Proxy-Connection: close") {
				fmt.Println(time.Now(), clientConn.RemoteAddr(), "=====http_error=====")
				if t == f.t {
					fmt.Println("try times:", t, "reach max try times")
					return
				}

				serverConn.Close()
				serverConn, err = net.Dial("tcp", remoteAddr)
				if err != nil {
					fmt.Printf("连接到远程服务器失败: %v\n", err)
					return
				}
				if cbr == nil {
					cbr = bytes.NewReader(cb.Bytes())
				}
				serverDown <- nil
				serverDown = make(chan interface{}, 5)
				sr = make(chan []byte, 5)
				sw = make(chan []byte)
				go serverRead(serverConn, sr, down, serverDown)
				go serverWrite(serverConn, sw, down, serverDown)

				buff, err := io.ReadAll(cbr)
				if err != nil {
					fmt.Println("read error:", err)
					return
				}
				cbr.Seek(0, io.SeekStart)
				sw <- buff
				write_buff = true

				i = 0
				t++
				sb.Reset()
				wsb = true
				write_head = false
				continue
			}
		} else { // https
		}
		if write_buff {
			write_buff = false
		} else {
			if !write_head && !wsb {
				cw <- []byte(sb.String())
				write_head = true
			} else {
				cw <- b
			}
		}
		if i < 10 {
			i++
		}
	}

}

func serverRead(serverConn net.Conn, sr chan []byte, down chan interface{}, serverDown chan interface{}) {
	defer func() {
		sr <- nil
		fmt.Println("server read end")
	}()
	for {
		b := make([]byte, 4096)
		// serverConn.SetReadDeadline(time.Now().Add(time.Duration(t) * time.Second))
		n, err := serverConn.Read(b)
		if err != nil {
			fmt.Println("server read error:", serverConn.LocalAddr(), err)
			return
		}
		select {
		case sr <- b[:n]:
		case <-down:
			down <- nil
			return
		case <-serverDown:
			serverDown <- nil
			return
		}
	}
}

func serverWrite(serverConn net.Conn, sw <-chan []byte, down chan interface{}, serverDown chan interface{}) {
	defer func() {
		serverConn.Close()
		fmt.Println("server write end")
	}()
	for {
		select {
		case b := <-sw:
			// fmt.Println("server write:", string(b))
			_, err := serverConn.Write(b)
			if err != nil {
				fmt.Printf("server write error: %v %v\n", serverConn.LocalAddr(), err)
				return
			}
		case <-down:
			down <- nil
			return
		case <-serverDown:
			serverDown <- nil
			return
		}
	}
}

type Flag struct {
	bh string // bind host. like localhost:10170
	ph string // proxy host. like localhost:20170
	v  bool   // version
	h  bool   // help
	t  int    // try times
}

func getArgs() *Flag {
	var f Flag
	flag.StringVar(&f.bh, "bh", "0.0.0.0:10171", "bind host.")
	flag.StringVar(&f.ph, "ph", "localhost:20171", "proxy host.")
	flag.BoolVar(&f.v, "v", false, "print version.")
	flag.BoolVar(&f.h, "h", false, "show help.")
	flag.IntVar(&f.t, "t", 10, "try times.")
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

		go handleConnection(clientConn, remoteAddr, f)
	}
}
