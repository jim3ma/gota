// Copyright © 2017 Jim Ma <majinjing3@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	_ "expvar"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/jim3ma/gota"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	log "github.com/Sirupsen/logrus"
)

// clientCmd represents the client command
var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Gota client",
	Long:  `Gota client`,
	Run: func(cmd *cobra.Command, args []string) {
		if viper.Get("mode") == "client" {
			log.Info("Gota: Work in client mode")
		} else {
			log.Error("Gota: Error mode in config")
		}

		logLevel := viper.GetString("log")
		gota.SetVerbose(viper.GetBool("verbose"))
		gota.SetLogLevel(logLevel)
		log.Info("Gota: Log Level: ", log.GetLevel())

		_, err := net.ResolveTCPAddr("tcp", viper.GetString("listen"))
		if err != nil {
			log.Errorf("Gota: Error Listen Address: %s", viper.GetString("listen"))
		}

		go func() {
			log.Debugln(http.ListenAndServe("localhost:6061", nil))
		}()

		userName := viper.GetString("auth.username")
		password := viper.GetString("auth.password")

		tunnel := viper.Get("tunnel")
		if t, ok := tunnel.([]interface{}); ok {
			clientConfig := make([]gota.TunnelActiveConfig, len(t))
			for i, v := range t {
				var config gota.TunnelActiveConfig
				if tt, ok := v.(map[interface{}]interface{}); ok {
					if raddr, ok := tt["remote"]; ok {
						config.RemoteAddr, _ = net.ResolveTCPAddr("tcp", fmt.Sprintf("%s", raddr))
					}
					if laddr, ok := tt["local"]; ok {
						config.LocalAddr, _ = net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:0", laddr))
					}
					if proxy, ok := tt["proxy"]; ok {
						config.Proxy, _ = url.Parse(fmt.Sprintf("%s", proxy))
					}
					clientConfig[i] = config
				} else {
					log.Errorf("Gota: Can't parse tunnel config: %s", v)
				}
			}
			client := gota.NewGota(clientConfig,
				&gota.TunnelAuthCredential{
					UserName: userName,
					Password: password,
				})

			if viper.GetBool("fastopen") {
				client.ConnManager.EnableFastOpen()
			}

			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

			wg := sync.WaitGroup{}
			go func() {
				sig := <-sigs
				log.Infof("Received signal: %s, exit gota client", sig)
				wg.Add(2)
				go func() {
					client.ConnManager.Stop()
					wg.Done()
				}()
				go func() {
					client.TunnelManager.Stop()
					wg.Done()
				}()
			}()

			client.ListenAndServe(viper.GetString("listen"))
			wg.Wait()
		} else {
			log.Error("Gota: Can't parse tunnel config")
		}
	},
}

func init() {
	RootCmd.AddCommand(clientCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// clientCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// clientCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

}
