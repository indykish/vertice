/*
** Copyright [2013-2015] [Megam Systems]
**
** Licensed under the Apache License, Version 2.0 (the "License");
** you may not use this file except in compliance with the License.
** You may obtain a copy of the License at
**
** http://www.apache.org/licenses/LICENSE-2.0
**
** Unless required by applicable law or agreed to in writing, software
** distributed under the License is distributed on an "AS IS" BASIS,
** WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
** See the License for the specific language governing permissions and
** limitations under the License.
 */
package docker

import (
	"encoding/json"
	log "code.google.com/p/log4go"
	"github.com/megamsys/megamd/global"
	"github.com/fsouza/go-dockerclient"
	"github.com/megamsys/megamd/provisioner"
	"github.com/tsuru/config"
	"github.com/megamsys/seru/cmd/seru"
	"github.com/megamsys/seru/cmd"
	"github.com/megamsys/libgo/db"
	"fmt"
	"strings"
)

func Init() {
	provisioner.Register("docker", &Docker{})
}

type Docker struct {
}

const BAREMETAL = "baremetal"

func (i *Docker) Create(assembly *global.AssemblyWithComponents, id string, instance bool, act_id string) (string, error) {
	//Creates containers into the specificed  endpoint provided in the assembly.
	log.Info("%q", assembly)
	pair_endpoint, perrscm := global.ParseKeyValuePair(assembly.Inputs, "endpoint")
	if perrscm != nil {
		log.Error("Failed to get the endpoint value : %s", perrscm)
		return "", perrscm
	}

	pair_img, perrscm := global.ParseKeyValuePair(assembly.Components[0].Inputs, "source")
	if perrscm != nil {
		log.Error("Failed to get the image value : %s", perrscm)
		return "", perrscm
	}
	
	pair_domain, perrdomain := global.ParseKeyValuePair(assembly.Components[0].Inputs, "domain")
	if perrdomain != nil {
		log.Error("Failed to get the image value : %s", perrdomain)
		return "", perrdomain
	}
	
	var endpoint string
	if pair_endpoint.Value == BAREMETAL {

		api_host, _ := config.GetString("swarm:host")
		endpoint = api_host

	} else {
		endpoint = pair_endpoint.Value
	}

	client, _ := docker.NewClient(endpoint)

	config := docker.Config{Image: pair_img.Value}
	copts := docker.CreateContainerOptions{Name: fmt.Sprint(assembly.Components[0].Name, ".", pair_domain.Value), Config: &config}

	container, conerr := client.CreateContainer(copts)
	if conerr != nil {
		log.Error("Container creation was failed : %s", conerr)
		return "", conerr
	}
    
	cont := &docker.Container{}
	mapP, _ := json.Marshal(container)
	json.Unmarshal([]byte(string(mapP)), cont)	
 
	serr := client.StartContainer(cont.ID, &docker.HostConfig{})
	if serr != nil {
		log.Error("Start container was failed : %s", serr)
		return "", serr
	}	
	
	inscontainer, _ := client.InspectContainer(cont.ID)
	contain := &docker.Container{}
	mapC, _ := json.Marshal(inscontainer)
	json.Unmarshal([]byte(string(mapC)), contain)
	
	container_network := &docker.NetworkSettings{}
	mapN, _ := json.Marshal(contain.NetworkSettings)
	json.Unmarshal([]byte(string(mapN)), container_network)
	fmt.Println(container_network.IPAddress)
	
	updatecomponent(assembly, container_network.IPAddress, cont.ID)
	
	herr := setHostName(fmt.Sprint(assembly.Components[0].Name, ".", pair_domain.Value), container_network.IPAddress)
	if herr != nil {
		log.Error("Failed to set the host name : %s", herr)
		return "", herr
	}	
	
	return "", nil
}

//Register a hostname on AWS route53 using seru 
func setHostName(name string, ip string) error {
	
	 s := make([]string, 4)
	 s = strings.Split(name, ".")
	 
	 accesskey, _ := config.GetString("aws:accesskey")
	 secretkey, _ := config.GetString("aws:secretkey")
		
	seru := &main.NewSubdomain{
							Accesskey: accesskey,
							Secretid:  secretkey,
							Domain:    fmt.Sprint(s[1], ".", s[2], "."),
							Subdomain: s[0], 
							Ip:        ip,
			}
	
	seruerr := seru.ApiRun(&cmd.Context{})
	if seruerr != nil {
		log.Error("Failed to seru run : %s", seruerr)
	}	
		
	return nil
}

// DeleteContainer kills a container, returning an error in case of failure.
func (i *Docker) Delete(assembly *global.AssemblyWithComponents, id string) (string, error) {

    pair_endpoint, perrscm := global.ParseKeyValuePair(assembly.Inputs, "endpoint")
	if perrscm != nil {
		log.Error("Failed to get the endpoint value : %s", perrscm)
	}
	
	pair_id, iderr := global.ParseKeyValuePair(assembly.Components[0].Outputs, "id")
	if iderr != nil {
		log.Error("Failed to get the endpoint value : %s", iderr)
	}
	
	var endpoint string
	if pair_endpoint.Value == BAREMETAL {

		api_host, _ := config.GetString("swarm:host")
		endpoint = api_host

	} else {
		endpoint = pair_endpoint.Value
	}
	
	client, _ := docker.NewClient(endpoint)
	kerr := client.KillContainer(docker.KillContainerOptions{ID: pair_id.Value})
	if kerr != nil {
		log.Error("Failed to kill the container : %s", kerr)
		return "", kerr
	}
	log.Info("Container was killed")
	return "", nil
}

func updatecomponent(assembly *global.AssemblyWithComponents, ipaddress string, id string) {
	log.Debug("Update process for component with ip and container id")
    mySlice := make([]*global.KeyValuePair, 2)
    mySlice[0] = &global.KeyValuePair{Key: "ip", Value: ipaddress}
    mySlice[1] = &global.KeyValuePair{Key: "id", Value: id}
   			
      
	update := global.Component{
		Id:						assembly.Components[0].Id,
		Name:					assembly.Components[0].Name,
		ToscaType:				assembly.Components[0].ToscaType,
		Inputs:					assembly.Components[0].Inputs,
		Outputs:    			mySlice,
		Artifacts:				assembly.Components[0].Artifacts,
		RelatedComponents:		assembly.Components[0].RelatedComponents,
		Operations:				assembly.Components[0].Operations,
		Status:					assembly.Components[0].Status,
		CreatedAt:				assembly.Components[0].CreatedAt,
		}
		
	conn, connerr := db.Conn("components")
	if connerr != nil {	
	    log.Error("Failed to riak connection : %s", connerr)
	}	
	      
	err := conn.StoreStruct(assembly.Components[0].Id, &update)	
	if err != nil {	
	    log.Error("Failed to store the update component data : %s", err)
	}	
	log.Info("Container component update was successfully.")
}