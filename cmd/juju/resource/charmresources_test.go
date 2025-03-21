// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package resource_test

import (
	"strings"

	"github.com/juju/charm/v9"
	charmresource "github.com/juju/charm/v9/resource"
	jujucmd "github.com/juju/cmd/v3"
	"github.com/juju/errors"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	jujuresource "github.com/juju/juju/cmd/juju/resource"
	resourcecmd "github.com/juju/juju/cmd/juju/resource"
	corecharm "github.com/juju/juju/core/charm"
)

var _ = gc.Suite(&CharmResourcesSuite{})

type CharmResourcesSuite struct {
	testing.IsolationSuite

	stub   *testing.Stub
	client *stubCharmStore
}

func (s *CharmResourcesSuite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)

	s.stub = &testing.Stub{}
	s.client = &stubCharmStore{stub: s.stub}
}

func (s *CharmResourcesSuite) TestInfo(c *gc.C) {
	var command resourcecmd.CharmResourcesCommand
	info := command.Info()

	c.Check(info, jc.DeepEquals, &jujucmd.Info{
		Name:    "charm-resources",
		Args:    "<charm>",
		Purpose: "Display the resources for a charm in a repository.",
		Aliases: []string{"list-charm-resources"},
		Doc: `
This command will report the resources and the current revision of each
resource for a charm in a repository.

<charm> can be a charm URL, or an unambiguously condensed form of it,
just like the deploy command.

Release is implied from the <charm> supplied. If not provided, the default
series for the model is used.

Channel can be specified with --channel.  If not provided, stable is used.

Where a channel is not supplied, stable is used.

Examples:

Display charm resources for the postgresql charm:
    juju charm-resources postgresql

Display charm resources for mycharm in the 2.0/edge channel:
    juju charm-resources mycharm --channel 2.0/edge
`,
		FlagKnownAs:    "option",
		ShowSuperFlags: []string{"show-log", "debug", "logging-config", "verbose", "quiet", "h", "help"},
	})
}

func (s *CharmResourcesSuite) TestOkay(c *gc.C) {
	resources := newCharmResources(c,
		"website:.tgz of your website",
		"music:mp3 of your backing vocals",
	)
	resources[0].Revision = 2
	s.client.ReturnListResources = [][]charmresource.Resource{resources}

	command := resourcecmd.NewCharmResourcesCommandForTest(s.client)
	code, stdout, stderr := runCmd(c, command, "cs:a-charm")
	c.Check(code, gc.Equals, 0)

	c.Check(stdout, gc.Equals, `
Resource  Revision
music     1
website   2

`[1:])
	c.Check(stderr, gc.Equals, "")
	s.stub.CheckCallNames(c,
		"ListResources",
	)
	s.stub.CheckCall(c, 0, "ListResources", []jujuresource.CharmID{
		{
			URL:     charm.MustParseURL("cs:a-charm"),
			Channel: corecharm.MustParseChannel("stable"),
		},
	})
}

func (s *CharmResourcesSuite) TestCharmhub(c *gc.C) {
	s.client.stub.SetErrors(errors.Errorf("charmhub charms are currently not supported"))

	command := resourcecmd.NewCharmResourcesCommandForTest(s.client)
	code, stdout, stderr := runCmd(c, command, "a-charm")
	c.Check(code, gc.Equals, 1)
	c.Check(stdout, gc.Equals, "")
	c.Check(stderr, gc.Equals, "ERROR charmhub charms are currently not supported\n")
}

func (s *CharmResourcesSuite) TestNoResources(c *gc.C) {
	s.client.ReturnListResources = [][]charmresource.Resource{{}}

	command := resourcecmd.NewCharmResourcesCommandForTest(s.client)
	code, stdout, stderr := runCmd(c, command, "cs:a-charm")
	c.Check(code, gc.Equals, 0)

	c.Check(stderr, gc.Equals, "No resources to display.\n")
	c.Check(stdout, gc.Equals, "")
	s.stub.CheckCallNames(c, "ListResources")
}

func (s *CharmResourcesSuite) TestOutputFormats(c *gc.C) {
	fp1, err := charmresource.GenerateFingerprint(strings.NewReader("abc"))
	c.Assert(err, jc.ErrorIsNil)
	fp2, err := charmresource.GenerateFingerprint(strings.NewReader("xyz"))
	c.Assert(err, jc.ErrorIsNil)
	resources := []charmresource.Resource{
		charmRes(c, "website", ".tgz", ".tgz of your website", string(fp1.Bytes())),
		charmRes(c, "music", ".mp3", "mp3 of your backing vocals", string(fp2.Bytes())),
	}
	s.client.ReturnListResources = [][]charmresource.Resource{resources}

	formats := map[string]string{
		"tabular": `
Resource  Revision
music     1
website   1

`[1:],
		"yaml": `
- name: music
  type: file
  path: music.mp3
  description: mp3 of your backing vocals
  revision: 1
  fingerprint: b0ea2a0f90267a8bd32848c65d7a61569a136f4e421b56127b6374b10a576d29e09294e620b4dcdee40f602115104bd5
  size: 48
  origin: store
- name: website
  type: file
  path: website.tgz
  description: .tgz of your website
  revision: 1
  fingerprint: 73100f01cf258766906c34a30f9a486f07259c627ea0696d97c4582560447f59a6df4a7cf960708271a30324b1481ef4
  size: 48
  origin: store
`[1:],
		"json": strings.Replace(""+
			"["+
			"  {"+
			`    "name":"music",`+
			`    "type":"file",`+
			`    "path":"music.mp3",`+
			`    "description":"mp3 of your backing vocals",`+
			`    "revision":1,`+
			`    "fingerprint":"b0ea2a0f90267a8bd32848c65d7a61569a136f4e421b56127b6374b10a576d29e09294e620b4dcdee40f602115104bd5",`+
			`    "size":48,`+
			`    "origin":"store"`+
			"  },{"+
			`    "name":"website",`+
			`    "type":"file",`+
			`    "path":"website.tgz",`+
			`    "description":".tgz of your website",`+
			`    "revision":1,`+
			`    "fingerprint":"73100f01cf258766906c34a30f9a486f07259c627ea0696d97c4582560447f59a6df4a7cf960708271a30324b1481ef4",`+
			`    "size":48,`+
			`    "origin":"store"`+
			"  }"+
			"]\n",
			"  ", "", -1),
	}
	for format, expected := range formats {
		c.Logf("checking format %q", format)
		command := resourcecmd.NewCharmResourcesCommandForTest(s.client)
		args := []string{
			"--format", format,
			"cs:a-charm",
		}
		code, stdout, stderr := runCmd(c, command, args...)
		c.Check(code, gc.Equals, 0)

		c.Check(stdout, gc.Equals, expected)
		c.Check(stderr, gc.Equals, "")
	}
}

func (s *CharmResourcesSuite) TestChannelFlag(c *gc.C) {
	fp1, err := charmresource.GenerateFingerprint(strings.NewReader("abc"))
	c.Assert(err, jc.ErrorIsNil)
	fp2, err := charmresource.GenerateFingerprint(strings.NewReader("xyz"))
	c.Assert(err, jc.ErrorIsNil)
	resources := []charmresource.Resource{
		charmRes(c, "website", ".tgz", ".tgz of your website", string(fp1.Bytes())),
		charmRes(c, "music", ".mp3", "mp3 of your backing vocals", string(fp2.Bytes())),
	}
	s.client.ReturnListResources = [][]charmresource.Resource{resources}
	command := resourcecmd.NewCharmResourcesCommandForTest(s.client)

	code, _, stderr := runCmd(c, command,
		"--channel", "development",
		"cs:a-charm",
	)

	c.Check(code, gc.Equals, 0)
	c.Check(stderr, gc.Equals, "")
	c.Check(resourcecmd.CharmResourcesCommandChannel(command), gc.Equals, "development")
}
