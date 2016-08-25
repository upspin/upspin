#!/bin/bash

# Copyright 2016 The Upspin Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

THIS=$$
SUDO=
case `uname` in
Linux) SUDO="sudo" ;;
esac
USER=tester@google.com
ROOT=testroot$THIS
USERROOT=$ROOT/$USER
BIN=/tmp/upspinfs.$THIS
LOG=/tmp/upspinfs$THIS.log

function startserver() {
	go build -o $BIN
	umount $ROOT 2>/dev/null
	mkdir $ROOT 2>/dev/null
	chmod 555 $ROOT
	echo log is $LOG
	$SUDO $BIN -log=debug -test=$USER $ROOT > $LOG 2>&1 &
}

function stopserver() {
	$SUDO umount $ROOT 2>/dev/null
	killall `basename $BIN` 2>/dev/null
	rm -f $BIN
	rm -f $LOG
	rm -fr $ROOT
}

function createUserRoot() {
	for i in `seq 10`
	do
		mkdir $USERROOT/ 2> /dev/null && return 0
		sleep 1
	done
	return 1
}

function runtests() {
	if ! createUserRoot $USER
	then
		echo "creating good user didn't work but should have" 1>&2
		return 1
	fi

	# Writing and reading files.
	cp ./test.sh $USERROOT/test.sh \
	&& cmp ./test.sh $USERROOT/test.sh \
	|| return 1

	# Creating subdirectories.
	mkdir $USERROOT/dir \
	|| return 1

	# Writing and reading into subdirectories.
	cp ./test.sh $USERROOT/dir/test.sh \
	&& cmp ./test.sh $USERROOT/dir/test.sh \
	|| return 1

	# Hard links (really copy on write).
	ln $USERROOT/test.sh $USERROOT/cow.sh \
	&& cmp $USERROOT/test.sh $USERROOT/cow.sh \
	|| return 1

	# Remove the first but the second remains.
	rm $USERROOT/test.sh || return 1
	if test -e $USERROOT/test.sh
	then
		echo rm $USERROOT/test.sh failed to remove
	fi
	cmp ./test.sh $USERROOT/cow.sh || return 1

	# Sym links.
	ln -s cow.sh $USERROOT/sym.sh \
	&& cmp $USERROOT/cow.sh $USERROOT/sym.sh \
	|| return 1

	# Remove the target and the symlink no longer works.
	rm $USERROOT/cow.sh || return 1
	if cmp test.sh $USERROOT/sym.sh 2>/dev/null
	then
		echo symlink target removed but symlink still returns bytes 1>&2
	fi
		
	return 0
}

startserver
trap stopserver EXIT
runtests
