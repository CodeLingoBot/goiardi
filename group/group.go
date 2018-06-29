/*
 * Copyright (c) 2013-2014, Jeremy Bingham (<jbingham@gmail.com>)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package group

import (
	"fmt"
	"github.com/ctdk/goiardi/acl"
	"github.com/ctdk/goiardi/actor"
	"github.com/ctdk/goiardi/client"
	"github.com/ctdk/goiardi/config"
	"github.com/ctdk/goiardi/datastore"
	"github.com/ctdk/goiardi/organization"
	"github.com/ctdk/goiardi/user"
	"github.com/ctdk/goiardi/util"
	"github.com/tideland/golib/logger"
	"net/http"
	"sync"
)

var DefaultGroups = [4]string{"admins", "billing-admins", "clients", "users"}
var DefaultUser = "pivotal" // should be moved out to config, I think. Same with
// acl

type Group struct {
	Name   string
	Org    *organization.Organization
	Actors []actor.Actor
	Groups []*Group
	m      sync.RWMutex
}

func New(org *organization.Organization, name string) (*Group, util.Gerror) {
	// will need to validate group name, presumably
	if name == "" {
		err := util.Errorf("Field 'name' missing")
		return nil, err
	}
	if !util.ValidateUserName(name) {
		err := util.Errorf("Field 'id' invalid")
		return nil, err
	}

	var found bool
	if config.UsingDB() {

	} else {
		ds := datastore.New()
		_, found = ds.Get(org.DataKey("group"), name)
	}
	if found {
		err := util.Errorf("Group %s in organization %s already exists", name, org.Name)
		err.SetStatus(http.StatusConflict)
		return nil, err
	}
	g := &Group{
		Name: name,
		Org:  org,
	}
	return g, nil
}

func Get(org *organization.Organization, name string) (*Group, util.Gerror) {
	if name == "" {
		err := util.Errorf("Field 'name' missing")
		return nil, err
	}
	if config.UsingDB() {

	}
	ds := datastore.New()
	g, found := ds.Get(org.DataKey("group"), name)
	var group *Group
	if g != nil {
		group = g.(*Group)
	}
	if !found {
		err := util.Errorf("group '%s' not found in organization %s", name, org.Name)
		err.SetStatus(http.StatusNotFound)
		return nil, err
	}
	return group, nil
}

func (g *Group) Save() util.Gerror {
	g.m.RLock()
	defer g.m.RUnlock()
	return g.save()
}

func (g *Group) Rename(newName string) util.Gerror {
	if !util.ValidateUserName(newName) {
		err := util.Errorf("Field 'id' invalid")
		return err
	}
	if newName == "" {
		err := util.Errorf("Field 'name' missing")
		return err
	}
	g.m.Lock()
	defer g.m.Unlock()
	if config.UsingDB() {

	} else {
		ds := datastore.New()
		if _, found := ds.Get(g.Org.DataKey("group"), newName); found {
			err := util.Errorf("Group %s already exists, cannot rename", newName)
			err.SetStatus(http.StatusConflict)
			return err
		}
		ds.Delete(g.Org.DataKey("group"), g.Name)
		g.Name = newName
		err := g.save()
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Group) save() util.Gerror {
	if config.UsingDB() {

	}
	ds := datastore.New()
	ds.Set(g.Org.DataKey("group"), g.Name, g)
	return nil
}

func (g *Group) Delete() util.Gerror {
	g.m.RLock()
	defer g.m.RUnlock()
	if config.UsingDB() {

	}
	ds := datastore.New()
	ds.Delete(g.Org.DataKey("group"), g.Name)
	ag := AllGroups(g.Org)
	for _, cg := range ag {
		j, _ := cg.checkForGroup(g.Name)
		if j {
			cg.DelGroup(g)
			cg.Save()
		}
	}
	return nil
}

func (g *Group) AddActor(a actor.Actor) util.Gerror {
	if found, _ := g.checkForActor(a.GetName()); !found {
		g.m.Lock()
		defer g.m.Unlock()
		g.Actors = append(g.Actors, a)
	}
	return nil
}

func (g *Group) DelActor(a actor.Actor) util.Gerror {
	if found, pos := g.checkForActor(a.GetName()); found {
		g.m.Lock()
		defer g.m.Unlock()
		g.Actors[pos] = nil
		g.Actors = append(g.Actors[:pos], g.Actors[pos+1:]...)
	} else {
		return util.Errorf("actor %s not in group", a.GetName())
	}
	return nil
}

func (g *Group) AddGroup(a *Group) util.Gerror {
	if found, _ := g.checkForGroup(a.Name); !found {
		g.m.Lock()
		defer g.m.Unlock()
		g.Groups = append(g.Groups, a)
	}
	return nil
}

func (g *Group) DelGroup(a *Group) util.Gerror {
	if found, pos := g.checkForGroup(a.Name); found {
		g.m.Lock()
		defer g.m.Unlock()
		g.Groups[pos] = nil
		g.Groups = append(g.Groups[:pos], g.Groups[pos+1:]...)
	} else {
		return util.Errorf("group %s not in group", a.GetName())
	}
	return nil
}

// Edit edits a group's membership en masse from JSON data listing the actors &
// groups that should be in the group, clearing the existing entries out
// entirely and adding everything back. This is not the preferred way, and
// hopefully this functionality will be able to be removed, but for the moment
// interoperability with mainstream Chef requires it.
func (g *Group) Edit(jsonData interface{}) util.Gerror {
	switch acts := jsonData.(type) {
	case map[string]interface{}:
		// presumably different once SQL mode catches up. Come back to
		// this later, when that's ready.
		actors := make([]actor.Actor, 0)
		groups := make([]*Group, 0)
		newActors := make(map[string]bool)
		newGroups := make(map[string]bool)
		oldMembers := make([]string, len(g.Actors) + len(g.Groups))
		for i, a := range g.Actors {
			oldMembers[i] = a
		}
		actLen := len(g.Actors)
		for i, og := range g.Groups {
			oldMembers[i+actLen] = og
		}

		if us, uok := acts["users"].([]interface{}); uok {
			for _, un := range us {
				unv, err := util.ValidateAsString(un)
				if err != nil {
					return err
				}
				u, err := user.Get(unv)
				if err != nil {
					return err
				}
				newActors[unv] = true
				actors = append(actors, u)
			}
		}
		if cs, cok := acts["clients"].([]interface{}); cok {
			for _, cn := range cs {
				cnv, err := util.ValidateAsString(cn)
				if err != nil {
					return err
				}
				c, err := client.Get(g.Org, cnv)
				if err != nil {
					return err
				}
				newActors[cnv] = true
				actors = append(actors, c)
			}
		}
		if grs, ok := acts["groups"].([]interface{}); ok {
			for _, gn := range grs {
				gnv, err := util.ValidateAsString(gn)
				if err != nil {
					return err
				}
				addGr, err := Get(g.Org, gnv)
				if err != nil {
					return err
				}
				newGroups[gnv] = true
				groups = append(groups, addGr)
			}
		}
		g.m.Lock()
		defer g.m.Unlock()
		g.Actors = actors
		g.Groups = groups

		// Remove any actors and groups from the relevant ACL grouping
		// if they aren't present anymore.
		toRemove := make([]acl.ACLMember, 0)
		for _, x := range oldMembers {
			if _, ok := newActors[x.GetName()]; !ok {
				if _, gok := newGroups[x.GetName()]; !gok {
					toRemove = append(toRemove, x)
				}
			}
		}
		if merr := acl.RemoveMembers(org, g, toRemove); merr != nil {
			return merr
		}

		// Add any new actors and groups to the ACL
		toAdd := make([]acl.ACLMember, 0, len(g.Actors) + len(g.Groups))
		toAdd = append(toAdd, g.Actors...)
		toAdd = append(toAdd, g.Groups...)

		err := g.save()
		if err != nil {
			return err
		}
	case nil:

	default:
		err := util.Errorf("invalid actors for group")
		return err
	}
	return nil
}

func (g *Group) ToJSON() map[string]interface{} {
	g.m.RLock()
	defer g.m.RUnlock()
	gJSON := make(map[string]interface{})
	gJSON["name"] = g.Name
	gJSON["groupname"] = g.Name
	gJSON["orgname"] = g.Org.Name
	gJSON["actors"] = make([]string, len(g.Actors))
	gJSON["users"] = make([]string, 0, len(g.Actors))
	gJSON["clients"] = make([]string, 0, len(g.Actors))
	for i, a := range g.Actors {
		gJSON["actors"].([]string)[i] = a.GetName()
		if a.IsClient() {
			gJSON["clients"] = append(gJSON["clients"].([]string), a.GetName())
		} else {
			gJSON["users"] = append(gJSON["users"].([]string), a.GetName())
		}
	}
	gJSON["groups"] = make([]string, len(g.Groups))
	for i, g := range g.Groups {
		gJSON["groups"].([]string)[i] = g.Name
	}

	return gJSON
}

func GetList(org *organization.Organization) []string {
	if config.UsingDB() {

	}
	ds := datastore.New()
	groupList := ds.GetList(org.DataKey("group"))
	return groupList
}

func AllGroups(org *organization.Organization) []*Group {
	if config.UsingDB() {

	}
	groupList := GetList(org)
	groups := make([]*Group, 0, len(groupList))
	for _, n := range groupList {
		g, err := Get(org, n)
		if err != nil {
			continue
		}
		groups = append(groups, g)
	}
	return groups
}

func ClearActor(org *organization.Organization, act actor.Actor) {
	if config.UsingDB() {

	}
	gs := AllGroups(org)
	for _, g := range gs {
		e := g.DelActor(act) // don't care if it's not available
		if e != nil {
			logger.Debugf("error deleting actor for %s: %s", act.GetName(), e.Error())
		}
		g.Save()
	}
}

func (g *Group) GetName() string {
	return g.Name
}

func (g *Group) URLType() string {
	return "groups"
}

func (g *Group) OrgName() string {
	return g.Org.Name
}

func (g *Group) ContainerType() string {
	return g.URLType()
}

func (g *Group) ContainerKind() string {
	return "containers"
}

func (g *Group) IsACLRole() bool {
	return true
}

func (g *Group) ACLName() string {
	return fmt.Sprintf("role_%s", g.Name)
}

// should this actually return the groups?

func MakeDefaultGroups(org *organization.Organization) util.Gerror {
	defUser, err := user.Get(DefaultUser)
	if err != nil {
		return err
	}
	for _, n := range DefaultGroups {
		g, err := New(org, n)
		if err != nil {
			return err
		}

		if n != "clients" && n != "billing-admins" {
			err = g.AddActor(defUser)
			if err != nil {
				return err
			}
		}

		err = g.Save()
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Group) checkForActor(name string) (bool, int) {
	for i, a := range g.Actors {
		if a.GetName() == name {
			return true, i
		}
	}
	return false, 0
}

func (g *Group) checkForGroup(name string) (bool, int) {
	for i, gr := range g.Groups {
		if gr.Name == name {
			return true, i
		}
	}
	return false, 0
}

func (g *Group) SeekActor(actr actor.Actor) bool {
	grs := make(map[string]*Group)
	var actChk func(gs *Group) bool
	actChk = func(gs *Group) bool {
		gs.m.RLock()
		defer gs.m.RUnlock()
		if f, _ := gs.checkForActor(actr.GetName()); f {
			return f
		}
		for _, gr := range gs.Groups {
			if _, ok := grs[gr.Name]; !ok {
				grs[gr.Name] = gr
				f := actChk(gr)
				if f {
					return f
				}
			}
		}
		return false
	}
	return actChk(g)
}
