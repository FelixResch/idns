package main

import (
	"github.com/miekg/dns"

	"log"
	"os"
	"os/signal"
	"syscall"
	"net"
	"github.com/go-redis/redis"
	"github.com/spf13/viper"
	"fmt"
)

func main() {

	viper.SetDefault("redisAddr", "127.0.0.1:6379")
	viper.SetDefault("redisPw", "")
	viper.SetDefault("redisDb", 0)

	viper.SetConfigName("config")
	viper.AddConfigPath("/etc/iserv/")
	viper.AddConfigPath("$HOME/.iserv")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("Fatal error reading config file %s \n", err))
	}

	client := redis.NewClient(&redis.Options{
		Addr: viper.GetString("redisArr"),
		Password: viper.GetString("redisPw"),
		DB: viper.GetInt("redisDb"),
	})

	setting, err := client.Get("config:ip").Result()

	if err == redis.Nil {
		setting = "172.16.0.1"
		log.Print("Could not read config from redis")
	} else {
		log.Print("Config read from redis")
	}

	handler := CustomHandler{
		client: client,
		serverIp: net.ParseIP(setting),
	}

	dns.ListenAndServe(setting + ":53", "udp", handler)

	log.Println("iDNS is listening")

	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	s := <-sig
	log.Fatalf("Signal (%v) received, stopping", s)
}

type CustomHandler struct {
	client *redis.Client
	serverIp net.IP
}

func (self CustomHandler) ServeDNS (w dns.ResponseWriter, r *dns.Msg) {
	for _, msg := range r.Question {
		m := new(dns.Msg)
		m.SetReply(r)
		if msg.Name == "master." {
			rr := &dns.A{
				Hdr: dns.RR_Header{Name:msg.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
				A: self.serverIp,
			}
			m.Answer = append(m.Answer, rr)

		} else {
			name := msg.Name[:len(msg.Name)-1]
			record, e := self.client.HGetAll("record:" + name).Result()
			if e == redis.Nil {
				m.Rcode = dns.RcodeNameError
				goto end
			}
			switch record["type"] {
			case "A":
				rr := &dns.A{
					Hdr: dns.RR_Header{Name:msg.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
					A: net.ParseIP(record["host"]),
				}
				m.Answer = append(m.Answer, rr)
			default:
				m.Rcode = dns.RcodeNameError
				goto end
			}
		}
		end:
		w.WriteMsg(m)
	}
}