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
	log "github.com/Sirupsen/logrus"
	"github.com/jim3ma/gota"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/url"
)

// clientCmd represents the client command
var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		// TODO: Work your own magic here
		if viper.Get("mode") == "client" {
			log.Info("Gota: Work in client mode")
		} else {
			log.Error("Gota: Error mode in config")
		}

		logLevel := viper.GetString("log")
		gota.SetLogLevel(logLevel)
		log.Info("Gota: Log Level: ", log.GetLevel())

		_, err := net.ResolveTCPAddr("tcp", viper.GetString("listen"))
		if err != nil {
			log.Errorf("Gota: Error Listen Address: %s", viper.GetString("listen"))
		}

		go func() {
			log.Println(http.ListenAndServe("localhost:6061", nil))
		}()

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
					log.Errorf("Gota: Cann't parse tunnel config: %s", v)
				}
			}
			client := gota.NewGota(clientConfig)
			client.ListenAndServe(viper.GetString("listen"))
		} else {
			log.Error("Gota: Cann't parse tunnel config")
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
