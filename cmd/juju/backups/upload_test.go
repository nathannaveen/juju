// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package backups_test

import (
	"archive/tar"
	"compress/gzip"
	"os"

	"github.com/juju/cmd/v3"
	"github.com/juju/cmd/v3/cmdtesting"
	"github.com/juju/errors"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/cmd/juju/backups"
)

type uploadSuite struct {
	BaseBackupsSuite
	command  cmd.Command
	filename string
}

var _ = gc.Suite(&uploadSuite{})

func (s *uploadSuite) SetUpTest(c *gc.C) {
	s.BaseBackupsSuite.SetUpTest(c)

	s.command = backups.NewUploadCommandForTest(s.store)
	s.filename = "juju-backup-20140912-130755.abcd-spam-deadbeef-eggs.tar.gz"
}

func (s *uploadSuite) TearDownTest(c *gc.C) {
	if err := os.Remove(s.filename); err != nil {
		if !os.IsNotExist(err) {
			c.Check(err, jc.ErrorIsNil)
		}
	}

	s.BaseBackupsSuite.TearDownTest(c)
}

func (s *uploadSuite) createArchive(c *gc.C) {
	archive, err := os.Create(s.filename)
	c.Assert(err, jc.ErrorIsNil)
	defer archive.Close()

	compressed := gzip.NewWriter(archive)
	defer compressed.Close()

	tarball := tar.NewWriter(compressed)
	defer tarball.Close()

	var files = []struct{ Name, Body string }{
		{"root.tar", "<state config files>"},
		{"dump/oplog.bson", "<something here>"},
	}
	for _, file := range files {
		hdr := &tar.Header{
			Name: file.Name,
			Size: int64(len(file.Body)),
		}
		err := tarball.WriteHeader(hdr)
		c.Assert(err, jc.ErrorIsNil)
		_, err = tarball.Write([]byte(file.Body))
		c.Assert(err, jc.ErrorIsNil)
	}
}

func (s *uploadSuite) TestOkay(c *gc.C) {
	s.createArchive(c)
	s.setSuccess()
	ctx, err := cmdtesting.RunCommand(c, s.command, s.filename)
	c.Check(err, jc.ErrorIsNil)

	c.Check(cmdtesting.Stderr(ctx), gc.Equals, "")
	c.Check(cmdtesting.Stdout(ctx), gc.Equals, "Uploaded backup file, creating backup ID spam\n")
}

func (s *uploadSuite) TestFileMissing(c *gc.C) {
	s.setSuccess()
	_, err := cmdtesting.RunCommand(c, s.command, s.filename)
	c.Check(os.IsNotExist(errors.Cause(err)), jc.IsTrue)
}

func (s *uploadSuite) TestError(c *gc.C) {
	s.createArchive(c)
	s.setFailure("failed!")
	_, err := cmdtesting.RunCommand(c, s.command, s.filename)
	c.Check(errors.Cause(err), gc.ErrorMatches, "failed!")
}
