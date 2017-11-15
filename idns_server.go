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
	"strconv"
	"math"
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
		Addr: viper.GetString("redisAddr"),
		Password: viper.GetString("redisPw"),
		DB: viper.GetInt("redisDb"),
	})

	setting, err := client.Get("config:ip").Result()

	log.Print("Read server ip ", setting, " from redis")

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

	err = dns.ListenAndServe(setting + ":53", "udp", handler)

	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}

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

func (c CustomHandler) ServeDNS (w dns.ResponseWriter, r *dns.Msg) {
	for _, msg := range r.Question {
		log.Print("Received query for ", msg.Name, " of type ", dns.TypeToString[msg.Qtype], "(", dns.ClassToString[msg.Qclass], ")")
		m := new(dns.Msg)
		m.SetReply(r)
		if msg.Name == "master." {
			rr := &dns.A{
				Hdr: dns.RR_Header{Name:msg.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
				A:   c.serverIp,
			}
			m.Answer = append(m.Answer, rr)

		} else {
			name := msg.Name[:len(msg.Name)-1]
			records, _ := c.client.Keys("record:*:" + name + ":*").Result()

			if len(records) == 0 {
				m.Rcode = dns.RcodeNameError
				goto end
			}
			for i := 0; i < len(records); i++ {
				recordKey := records[i]
				record, e := c.client.HGetAll(recordKey).Result()
				if e == redis.Nil {
					panic("Invalid DNS state")
					goto end
				}
				switch record["type"] {
				case "A":
					rr := &dns.A{
						Hdr: dns.RR_Header{Name:msg.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
						A: net.ParseIP(record["host"]),
					}
					m.Answer = append(m.Answer, rr)
				case "CNAME":
					rr := &dns.CNAME{
						Hdr: dns.RR_Header{Name: msg.Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 0},
						Target: record["host"],
					}
					m.Answer = append(m.Answer, rr)
				case "SRV":
					port, e := strconv.ParseInt(record["port"], 10, 32)
					if e != nil {
						log.Fatal(e)
						m.Rcode = dns.RcodeServerFailure
						goto end
					}
					if port > math.MaxUint16 {
						log.Fatal("Port ", port, " bigger than max port: ", math.MaxUint16)
						m.Rcode = dns.RcodeServerFailure
						goto end
					}
					rr := &dns.SRV{
						Hdr: dns.RR_Header{Name: msg.Name, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: 0},
						Target: record["host"],
						Port: uint16(port),
						Priority: 1,
						Weight: 1,
					}
					m.Answer = append(m.Answer, rr)
					keys, _ := c.client.Keys("record:*:" + record["host"] + ":A").Result()
					if len(keys) > 0 {
						for _, key := range keys {
							record, e = c.client.HGetAll(key).Result()
							if e == nil {
								arr := &dns.A{
									Hdr: dns.RR_Header{Name: msg.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
									A: net.ParseIP(record["host"]),
								}
								m.Answer = append(m.Answer, arr)
							} else {
								log.Print(key, ": ", e)
							}
						}
					}
				default:
					m.Rcode = dns.RcodeNameError
					goto end
				}
			}
		}
		log.Print("Sending ", m.String())
		end:
		w.WriteMsg(m)
	}
}