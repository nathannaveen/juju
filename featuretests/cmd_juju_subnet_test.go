// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package featuretests

import (
	"github.com/juju/cmd/v3"
	"github.com/juju/cmd/v3/cmdtesting"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/core/network"
	jujutesting "github.com/juju/juju/juju/testing"
	"github.com/juju/juju/state"
)

type cmdSubnetSuite struct {
	jujutesting.JujuConnSuite
}

func (s *cmdSubnetSuite) AddSubnet(c *gc.C, info network.SubnetInfo) *state.Subnet {
	subnet, err := s.State.AddSubnet(info)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(subnet.CIDR(), gc.Equals, info.CIDR)
	return subnet
}

func (s *cmdSubnetSuite) AddSpace(c *gc.C, name string, ids []string, public bool) *state.Space {
	space, err := s.State.AddSpace(name, "", ids, public)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(space.Name(), gc.Equals, name)
	subnets, err := space.Subnets()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(subnets, gc.HasLen, len(ids))
	return space
}

func (s *cmdSubnetSuite) Run(c *gc.C, expectedError string, args ...string) *cmd.Context {
	context, err := runCommand(c, args...)
	if expectedError != "" {
		c.Assert(err, gc.ErrorMatches, expectedError)
	} else {
		c.Assert(err, jc.ErrorIsNil)
	}
	return context
}

func (s *cmdSubnetSuite) RunAdd(c *gc.C, expectedError string, args ...string) (string, string, error) {
	cmdArgs := append([]string{"add-subnet"}, args...)
	ctx, err := runCommand(c, cmdArgs...)
	stdout, stderr := "", ""
	if ctx != nil {
		stdout = cmdtesting.Stdout(ctx)
		stderr = cmdtesting.Stderr(ctx)
	}
	if expectedError != "" {
		c.Assert(err, gc.NotNil)
		c.Assert(stderr, jc.Contains, expectedError)
	}
	return stdout, stderr, err
}

func (s *cmdSubnetSuite) AssertOutput(c *gc.C, context *cmd.Context, expectedOut, expectedErr string) {
	c.Assert(cmdtesting.Stdout(context), gc.Equals, expectedOut)
	c.Assert(cmdtesting.Stderr(context), gc.Equals, expectedErr)
}

func (s *cmdSubnetSuite) TestSubnetAddNoArguments(c *gc.C) {
	expectedError := "invalid arguments specified: either CIDR or provider ID is required"
	s.Run(c, expectedError, "add-subnet")
}

func (s *cmdSubnetSuite) TestSubnetAddInvalidCIDRTakenAsProviderId(c *gc.C) {
	expectedError := "invalid arguments specified: space name is required"
	s.Run(c, expectedError, "add-subnet", "subnet-xyz")
}

func (s *cmdSubnetSuite) TestSubnetAddCIDRAndInvalidSpaceName(c *gc.C) {
	expectedError := `invalid arguments specified: " f o o " is not a valid space name`
	s.Run(c, expectedError, "add-subnet", "10.0.0.0/8", " f o o ")
}

func (s *cmdSubnetSuite) TestSubnetAddAlreadyExistingCIDR(c *gc.C) {
	space := s.AddSpace(c, "foo", nil, true)
	s.AddSubnet(c, network.SubnetInfo{CIDR: "0.10.0.0/24", SpaceID: space.Id(), ProviderId: "dummy-private"})

	expectedError := `cannot add subnet: adding subnet "0.10.0.0/24": subnet "0.10.0.0/24" already exists`
	s.RunAdd(c, expectedError, "0.10.0.0/24", "foo")
}

func (s *cmdSubnetSuite) TestSubnetAddValidCIDRUnknownByTheProvider(c *gc.C) {
	expectedError := `cannot add subnet: subnet with CIDR "10.0.0.0/8" not found`
	s.RunAdd(c, expectedError, "10.0.0.0/8", "myspace")
}

func (s *cmdSubnetSuite) TestSubnetAddWithUnknownSpace(c *gc.C) {
	expectedError := `cannot add subnet: space "myspace" not found`
	s.RunAdd(c, expectedError, "0.10.0.0/24", "myspace")
}

func (s *cmdSubnetSuite) TestSubnetAddWithoutZonesWhenProviderHasZones(c *gc.C) {
	s.AddSpace(c, "myspace", nil, true)

	context := s.Run(c, expectedSuccess, "add-subnet", "0.10.0.0/24", "myspace")
	s.AssertOutput(c, context,
		"", // no stdout output
		"added subnet with CIDR \"0.10.0.0/24\" in space \"myspace\"\n",
	)

	subnet, err := s.State.SubnetByCIDR("0.10.0.0/24")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(subnet.CIDR(), gc.Equals, "0.10.0.0/24")
	c.Assert(subnet.SpaceName(), gc.Equals, "myspace")
	c.Assert(subnet.ProviderId(), gc.Equals, network.Id("dummy-private"))
	c.Assert(subnet.AvailabilityZones(), gc.DeepEquals, []string{"zone1", "zone2"})
}

func (s *cmdSubnetSuite) TestSubnetAddWithUnavailableZones(c *gc.C) {
	s.AddSpace(c, "myspace", nil, true)

	expectedError := `cannot add subnet: Zones contain unavailable zones: "zone2"`
	s.RunAdd(c, expectedError, "dummy-private", "myspace", "zone1", "zone2")
}

func (s *cmdSubnetSuite) TestSubnetAddWithZonesWithNoProviderZones(c *gc.C) {
	s.AddSpace(c, "myspace", nil, true)

	context := s.Run(c, expectedSuccess, "add-subnet", "dummy-public", "myspace", "zone1")
	s.AssertOutput(c, context,
		"", // no stdout output
		"added subnet with ProviderId \"dummy-public\" in space \"myspace\"\n",
	)

	subnet, err := s.State.SubnetByCIDR("0.20.0.0/24")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(subnet.CIDR(), gc.Equals, "0.20.0.0/24")
	c.Assert(subnet.SpaceName(), gc.Equals, "myspace")
	c.Assert(subnet.ProviderId(), gc.Equals, network.Id("dummy-public"))
	c.Assert(subnet.AvailabilityZones(), gc.DeepEquals, []string{"zone1"})
}

func (s *cmdSubnetSuite) TestSubnetListNoResults(c *gc.C) {
	context := s.Run(c, expectedSuccess, "list-subnets")
	s.AssertOutput(c, context,
		"", // no stdout output
		"No subnets to display.\n",
	)
}

func (s *cmdSubnetSuite) TestSubnetListResultsWithFilters(c *gc.C) {
	space := s.AddSpace(c, "myspace", nil, true)
	s.AddSubnet(c, network.SubnetInfo{
		CIDR: "10.0.0.0/8",
	})
	s.AddSubnet(c, network.SubnetInfo{
		CIDR:              "10.10.0.0/16",
		AvailabilityZones: []string{"zone1"},
		SpaceID:           space.Id(),
	})

	context := s.Run(c,
		expectedSuccess,
		"subnets", "--zone", "zone1", "--space", "myspace",
	)
	c.Assert(cmdtesting.Stderr(context), gc.Equals, "") // no stderr expected
	stdout := cmdtesting.Stdout(context)
	c.Assert(stdout, jc.Contains, "subnets:")
	c.Assert(stdout, jc.Contains, "10.10.0.0/16:")
	c.Assert(stdout, jc.Contains, "space: myspace")
	c.Assert(stdout, jc.Contains, "zones:")
	c.Assert(stdout, jc.Contains, "- zone1")
	c.Assert(stdout, gc.Not(jc.Contains), "10.0.0.0/8:")
}
