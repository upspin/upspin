#!/bin/bash
set -x

# Copyright 2016 The Upspin Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

ROOT=$1
USER=$2
USERROOT=$ROOT/$USER

mkdir $USERROOT/ || exit 1

# Writing and reading files.
cp ./test.sh $USERROOT/test.sh \
&& cmp ./test.sh $USERROOT/test.sh \
|| exit 1

# Creating subdirectories.
mkdir $USERROOT/dir \
|| exit 1

# Writing and reading into subdirectories.
cp ./test.sh $USERROOT/dir/test.sh \
&& cmp ./test.sh $USERROOT/dir/test.sh \
|| exit 1

# Hard links (really copy on write).
ln $USERROOT/test.sh $USERROOT/cow.sh \
&& cmp $USERROOT/test.sh $USERROOT/cow.sh \
|| exit 1

# Remove the first but the second remains.
rm $USERROOT/test.sh || exit 1
if test -e $USERROOT/test.sh
then
	echo rm $USERROOT/test.sh failed to remove
fi
cmp ./test.sh $USERROOT/cow.sh || exit 1

# Sym links.
ln -s cow.sh $USERROOT/sym.sh \
&& cmp $USERROOT/cow.sh $USERROOT/sym.sh \
|| exit 1

# Remove the target and the symlink no longer works.
rm $USERROOT/cow.sh || exit 1
if cmp test.sh $USERROOT/sym.sh 2>/dev/null
then
	echo symlink target removed but symlink still exits bytes 1>&2
fi

exit 0
