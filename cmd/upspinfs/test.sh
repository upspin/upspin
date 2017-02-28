#!/bin/bash
set -x
set -e

# Copyright 2016 The Upspin Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

ROOT=$1
USER=$2
USERROOT=$ROOT/$USER

mkdir $USERROOT/

# Writing and reading files.
cp ./test.sh $USERROOT/test.sh
cmp ./test.sh $USERROOT/test.sh

# Creating subdirectories.
mkdir $USERROOT/dir

# Writing and reading into subdirectories.
cp ./test.sh $USERROOT/dir/test.sh
cmp ./test.sh $USERROOT/dir/test.sh

# Sym links.
ln -s test.sh $USERROOT/sym.sh
cmp $USERROOT/test.sh $USERROOT/sym.sh

# Remove the target and the symlink no longer works.
rm $USERROOT/test.sh
if cmp ./test.sh $USERROOT/sym.sh 2>/dev/null
then
	echo symlink target removed but symlink still works 1>&2
	exit 1
fi

# Rename a file (target doesn't exist).
cp ./test.sh $USERROOT/oldname.sh
mv $USERROOT/oldname.sh $USERROOT/newname.sh
cmp ./test.sh $USERROOT/newname.sh
test ! -e $USERROOT/oldname.sh

# Rename a file (target does exist).
echo sdfasdasdf > $USERROOT/newname.sh
cp ./test.sh $USERROOT/oldname.sh
mv $USERROOT/oldname.sh $USERROOT/newname.sh
cmp ./test.sh $USERROOT/newname.sh

# Hard links are not working in Linux right now.  Avoid until this is fixed.
case $(uname) in
Linux) exit 0 ;;
esac

# Hard links (really copy on write).
cp ./test.sh $USERROOT/test.sh
ln $USERROOT/test.sh $USERROOT/cow.sh
cmp $USERROOT/test.sh $USERROOT/cow.sh

# Remove the first but the second remains.
rm $USERROOT/test.sh
if test -e $USERROOT/test.sh
then
	echo rm $USERROOT/test.sh failed to remove 1>&2
	exit 1
fi
cmp ./test.sh $USERROOT/cow.sh

# Test access control.  Put a file in a new directory
# and then cut off create and write access.
mkdir $USERROOT/limited
cp ./test.sh $USERROOT/limited/test.sh
echo "r,l: $USER" > $USERROOT/limited/Access

# Create should fail.
if echo > $USERROOT/limited/failedcreate
then
	echo "echo > $USERROOT/limited/failedcreate" should have failed 1>&2
	exit 1
fi

# Rewrite should fail.
if echo > $USERROOT/limited/test.sh
then
	echo "echo > $USERROOT/limited/test.sh" should have failed 1>&2
	exit 1
fi

#  Append should fail.
if echo >> $USERROOT/limited/test.sh
then
	echo "echo >> $USERROOT/limited/test.sh" should have failed 1>&2
	exit 1
fi

exit 0
