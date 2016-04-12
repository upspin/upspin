package testsetup

import (
	"testing"

	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
)

func TestInProcess(t *testing.T) {
	const (
		userName = "testuser@testdomain.com"
		content2 = "yo! file2"
	)
	context, err := NewContextForUser(upspin.UserName(userName), upspin.EEp256Pack)
	if err != nil {
		t.Fatal(err)
	}
	client, err := InProcess(context)
	if err != nil {
		t.Fatal(err)
	}
	InstallUserRoot(context)

	testSetup := Setup{
		Tree: Tree{
			N("Dir1/", ""),
			N("Dir1/file1.txt", "yo! file1"),
			N("Dir2/", ""),
			N("Dir2/file2.txt", content2),
		},
	}

	err = MakeTree(client, userName, testSetup)
	if err != nil {
		t.Fatal(err)
	}

	// Now check the tree was setup correctly
	de, err := context.Directory.Glob(userName + "/*")
	if err != nil {
		t.Fatal(err)
	}
	expectedDirs := []upspin.PathName{upspin.PathName(userName + "/Dir1"), upspin.PathName(userName + "/Dir2")}
	if len(de) != len(expectedDirs) {
		t.Errorf("Expected %d dir entries, got %d", len(expectedDirs), len(de))
	}
	for i := 0; i < len(expectedDirs); i++ {
		if de[i].Name != expectedDirs[i] {
			t.Errorf("Expected entry %s, got %s", expectedDirs[i], de[i].Name)
		}
	}
	data, err := client.Get(upspin.PathName(userName + "/Dir2/file2.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content2 {
		t.Errorf("Expected content %q, got %q", content2, data)
	}
}
