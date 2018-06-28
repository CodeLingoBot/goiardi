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

package acl

import (
	"encoding/gob"
	"fmt"
	"github.com/casbin/casbin"
	"github.com/ctdk/goiardi/association"
	"github.com/ctdk/goiardi/config"
	"github.com/ctdk/goiardi/group"
	"github.com/ctdk/goiardi/indexer"
	"github.com/ctdk/goiardi/organization"
	"github.com/ctdk/goiardi/role"
	"github.com/ctdk/goiardi/user"
	"io/ioutil"
	"os"
	"strings"
	"testing"
)

var pivotal *user.User
var orgCount int

func init() {
	gob.Register(new(organization.Organization))
	gob.Register(new(user.User))
	gob.Register(new(association.Association))
	gob.Register(new(association.AssociationReq))
	gob.Register(new(group.Group))
	gob.Register(new(role.Role))
	gob.Register(make(map[string]interface{}))
	indexer.Initialize(config.Config)
	config.Config.UseAuth = true
}

func setup() {
	confDir, err := ioutil.TempDir("", "acl-test")
	if err != nil {
		panic(err)
	}
	config.Config.PolicyRoot = confDir
	pivotal, _ = user.New("pivotal")
	pivotal.Admin = true
	pivotal.Save()
}

func teardown() {
	os.RemoveAll(config.Config.PolicyRoot)
}

func buildOrg() (*organization.Organization, *user.User, *casbin.SyncedEnforcer) {
	adminUser, _ := user.New(fmt.Sprintf("admin%d", orgCount))
	adminUser.Admin = true
	adminUser.Save()
	org, _ := organization.New(fmt.Sprintf("org%d", orgCount), fmt.Sprintf("test org %d", orgCount))
	orgCount++
	ar, _ := association.SetReq(adminUser, org, pivotal)
	ar.Accept()
	group.MakeDefaultGroups(org)
	admins, _ := group.Get(org, "admins")
	admins.AddActor(adminUser)
	admins.Save()

	// m := casbin.NewModel(modelDefinition)
	// e, _ := initializeACL(org, m)
	loadACL(org)
	e := orgEnforcers[org.Name]
	// temporary
	e.AddGroupingPolicy(adminUser.Username, "admins")

	return org, adminUser, e
}

func TestMain(m *testing.M) {
	setup()
	r := m.Run()
	if r == 0 {
		teardown()
	}
	os.Exit(r)
}

func TestInitACL(t *testing.T) {
	org, _ := organization.New("florp", "mlorph normph")
	group.MakeDefaultGroups(org)

	m := casbin.NewModel(modelDefinition)
	e, err := initializeACL(org, m)
	if err != nil {
		t.Error(err)
	}

	e.AddGroupingPolicy("test1", "admins")
	e.AddGroupingPolicy("test_user", "users")

	testingPolicies := [][]string{
		{"true", "test1", "groups", "containers", "default", "create", "allow"},
		{"true", "pivotal", "groups", "containers", "default", "create", "allow"},
		{"true", "test1", "clients", "containers", "default", "read", "allow"},
		{"false", "test_user", "groups", "containers", "default", "read", "allow"},
		{"true", "test_user", "roles", "containers", "default", "read", "allow"},
		{"false", "test_user", "roles", "containers", "default", "nonexistent_perm", "allow"},
	}

	for _, policy := range testingPolicies {
		var expected bool
		if policy[0] == "true" {
			expected = true
		}
		enforceP := make([]interface{}, len(policy[1:]))
		for i, v := range policy[1:] {
			enforceP[i] = v
		}
		z := e.Enforce(enforceP...)
		if z != expected {
			t.Errorf("Expected '%s' to evaluate as %v, got %v", strings.Join(policy[1:], ", "), expected, z)
		}
	}
	r := e.GetRolesForUser("test1")
	if r[0] != "admins" {
		t.Errorf("test1 user should have been a member of the 'admins' group, but wasn't. These roles were found instead: %v", r)
	}
}

func TestCheckItemPerm(t *testing.T) {
	org, adminUser, e := buildOrg()
	r, _ := role.New(org, "chkitem")
	r.Save()
	chk, err := CheckItemPerm(org, r, adminUser, "create")
	if err != nil {
		t.Errorf("ChkItemPerm for role with adminUser failed: %s", err.Error())
	}
	if !chk {
		t.Errorf("ChkItemPerm for role with adminUser should have been true, but was false.")
	}
	u, _ := user.New("test_user")
	u.Save()
	ar, _ := association.SetReq(u, org, adminUser)
	ar.Accept()
	us, _ := group.Get(org, "users")
	us.AddActor(u)
	us.Save()
	// temporary again
	e.AddGroupingPolicy(u.Username, "users")

	chk, err = CheckItemPerm(org, r, u, "create")
	if err != nil {
		t.Errorf("ChkItemPerm for role with normal user failed: %s", err.Error())
	}
	if !chk {
		t.Errorf("ChkItemPerm for role with normal user should have been true, but was false.")
	}
	chk, err = CheckItemPerm(org, r, u, "grant")
	if err != nil {
		t.Errorf("ChkItemPerm for role with normal user failed with an error (should have failed without one): %s", err.Error())
	}
	if chk {
		t.Errorf("ChkItemPerm for role with normal user should have been false, but was true.")
	}

	chk, err = CheckItemPerm(org, r, u, "frobnatz")
	if err == nil {
		t.Error("ChkItemPerm for role with normal user with a non-existent perm failed without an error (should have failed with one)")
	}
	if chk {
		t.Errorf("ChkItemPerm for role with normal user with a non-existent perm should have been false, but was true.")
	}

	chk, err = CheckItemPerm(org, r, adminUser, "frobnatz")
	if err == nil {
		t.Error("ChkItemPerm for role with admin user with a non-existent perm failed without an error (should have failed with one)")
	}
	if chk {
		t.Errorf("ChkItemPerm for role with admin user with a non-existent perm should have been false, but was true.")
	}
}

func TestClients(t *testing.T) {

}

func TestMultipleOrgs(t *testing.T) {

}
