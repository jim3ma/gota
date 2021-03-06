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
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"

	log "github.com/Sirupsen/logrus"
	"github.com/jim3ma/gota"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// serverCmd represents the server command
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Gota server",
	Long:  `Gota server`,
	Run: func(cmd *cobra.Command, args []string) {
		if viper.Get("mode") == "server" {
			log.Info("Gota: Work in server mode")
		} else {
			log.Error("Gota: Error mode in config")
		}

		logLevel := viper.GetString("log")
		gota.SetVerbose(viper.GetBool("verbose"))
		gota.SetLogLevel(logLevel)
		log.Info("Gota: Log Level: ", log.GetLevel())

		_, err := net.ResolveTCPAddr("tcp", viper.GetString("remote"))
		if err != nil {
			log.Errorf("Gota: Error Remote Address: %s", viper.GetString("remote"))
		}

		go func() {
			log.Debugln(http.ListenAndServe("localhost:6060", nil))
		}()

		userName := viper.GetString("auth.username")
		password := viper.GetString("auth.password")

		whiteIPs := viper.GetStringSlice("whiteips")

		tunnel := viper.Get("tunnel")
		if t, ok := tunnel.([]interface{}); ok {
			clientConfig := make([]gota.TunnelPassiveConfig, len(t))
			for i, v := range t {
				var config gota.TunnelPassiveConfig
				if tt, ok := v.(map[interface{}]interface{}); ok {
					if addr, ok := tt["listen"]; ok {
						config.TCPAddr, _ = net.ResolveTCPAddr("tcp", fmt.Sprintf("%s", addr))
					}
					clientConfig[i] = config
				} else {
					log.Errorf("Gota: Cann't parse tunnel config: %s", v)
				}
			}

			client := gota.NewGota(clientConfig,
				&gota.TunnelAuthCredential{
					UserName: userName,
					Password: password,
				})

			client.TunnelManager.SetWhiteIPs(whiteIPs)
			if viper.GetBool("fastopen") {
				client.ConnManager.EnableFastOpen()
			}

			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

			wg := sync.WaitGroup{}
			go func() {
				sig := <-sigs
				log.Infof("Received signal: %s, exit gota server", sig)
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

			if viper.GetBool("pool.enable") {
				client.ConnManager.SetConnPool(viper.GetInt("pool.count"), viper.GetInt("pool.keepalive"))
			}
			client.Serve(viper.GetString("remote"))
			wg.Wait()

		} else {
			log.Error("Gota: Can not parse tunnel config")
		}
	},
}

func init() {
	RootCmd.AddCommand(serverCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// serverCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// serverCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

}
