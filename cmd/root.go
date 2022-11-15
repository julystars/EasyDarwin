package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EasyDarwin/EasyDarwin/models"
	"github.com/EasyDarwin/EasyDarwin/routers"
	"github.com/EasyDarwin/EasyDarwin/rtsp"
	"github.com/penggy/EasyGoLib/db"
	"github.com/penggy/EasyGoLib/utils"
	"github.com/spf13/cobra"
)

type program struct {
	httpPort   int
	httpServer *http.Server
	rtspPort   int
	rtspServer *rtsp.Server
}

func (p *program) StopHTTP() (err error) {
	if p.httpServer == nil {
		err = fmt.Errorf("HTTP Server Not Found")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = p.httpServer.Shutdown(ctx); err != nil {
		return
	}
	return
}

func (p *program) StartHTTP() (err error) {
	p.httpServer = &http.Server{
		Addr:              fmt.Sprintf(":%d", p.httpPort),
		Handler:           routers.Router,
		ReadHeaderTimeout: 5 * time.Second,
	}
	link := fmt.Sprintf("http://%s:%d", utils.LocalIP(), p.httpPort)
	log.Println("http server start -->", link)
	go func() {
		if err := p.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Println("start http server error", err)
		}
		log.Println("http server end")
	}()
	return
}

func (p *program) StartRTSP() (err error) {
	if p.rtspServer == nil {
		err = fmt.Errorf("RTSP Server Not Found")
		return
	}
	sport := ""
	if p.rtspPort != 554 {
		sport = fmt.Sprintf(":%d", p.rtspPort)
	}
	link := fmt.Sprintf("rtsp://%s%s", utils.LocalIP(), sport)
	log.Println("rtsp server start -->", link)
	go func() {
		if err := p.rtspServer.Start(); err != nil {
			log.Println("start rtsp server error", err)
		}
		log.Println("rtsp server end")
	}()
	return
}

func (p *program) StopRTSP() (err error) {
	if p.rtspServer == nil {
		err = fmt.Errorf("RTSP Server Not Found")
		return
	}
	p.rtspServer.Stop()
	return
}

func (p *program) Start() (err error) {
	log.Println("********** START **********")
	if utils.IsPortInUse(p.httpPort) {
		err = fmt.Errorf("HTTP port[%d] In Use", p.httpPort)
		return
	}
	if utils.IsPortInUse(p.rtspPort) {
		err = fmt.Errorf("RTSP port[%d] In Use", p.rtspPort)
		return
	}
	err = models.Init()
	if err != nil {
		return
	}
	err = routers.Init()
	if err != nil {
		return
	}
	p.StartRTSP()
	p.StartHTTP()

	if !utils.Debug {
		log.Println("log files -->", utils.LogDir())
		log.SetOutput(utils.GetLogWriter())
	}
	go func() {
		for range routers.API.RestartChan {
			p.StopHTTP()
			p.StopRTSP()
			utils.ReloadConf()
			p.StartRTSP()
			p.StartHTTP()
		}
	}()

	go func() {
		log.Printf("demon pull streams")
		for {
			var streams []models.Stream
			db.SQLite.Find(&streams)
			if err := db.SQLite.Find(&streams).Error; err != nil {
				log.Printf("find stream err:%v", err)
				return
			}
			for i := len(streams) - 1; i > -1; i-- {
				v := streams[i]
				if rtsp.GetServer().GetPusher(v.CustomPath) != nil {
					continue
				}
				agent := fmt.Sprintf("EasyDarwinGo/%s", routers.BuildVersion)
				if routers.BuildDateTime != "" {
					agent = fmt.Sprintf("%s(%s)", agent, routers.BuildDateTime)
				}
				client, err := rtsp.NewRTSPClient(rtsp.GetServer(), v.URL, int64(v.HeartbeatInterval)*1000, agent)
				if err != nil {
					continue
				}
				client.CustomPath = v.CustomPath
				client.DeviceName = v.DeviceName

				err = client.Start(time.Duration(v.IdleTimeout) * time.Second)
				if err != nil {
					log.Printf("Pull stream err :%v", err)
					continue
				}
				pusher := rtsp.NewClientPusher(client)
				rtsp.GetServer().AddPusher(pusher)
				//streams = streams[0:i]
				//streams = append(streams[:i], streams[i+1:]...)
			}
			time.Sleep(10 * time.Second)
		}
	}()
	return
}

func (p *program) Stop() (err error) {
	defer log.Println("********** STOP **********")
	defer utils.CloseLogWriter()
	p.StopHTTP()
	p.StopRTSP()
	models.Close()
	return
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "EasyDarwin",
	Short: "EasyDarwin",
	Long:  "EasyDarwin controls the device lifecycle.",
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
	Run: func(cmd *cobra.Command, args []string) {
		sig := make(chan os.Signal)
		signal.Notify(sig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

		log.Println("进程启动...")
		httpPort := utils.Conf().Section("http").Key("port").MustInt(8181)
		rtspServer := rtsp.GetServer()
		p := &program{
			httpPort:   httpPort,
			rtspPort:   rtspServer.TCPPort,
			rtspServer: rtspServer,
		}
		p.Start()

		go func() {
			<-sig
			log.Println("exiting...")
			p.Stop()
			log.Println("exited.")
			os.Exit(0)
		}()

		for {
			time.Sleep(60 * time.Second)
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func init() {}
