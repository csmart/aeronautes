package local

import (
	"crypto/md5"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// The operator acknowledged replacing a particular object. If that object is
// gone by the time the run looks, the run records that there was nothing there
// — and then a name recreated before the publication used to be overwritten
// anyway, with no conditional and no acknowledgement of THAT object, while the
// ledger said there had been nothing to replace (ADR 0085).
//
// The two routes are refused by different mechanisms and both are here. The
// native route has no later read, so what refuses it is the create conditional
// the publication now carries, enforced by the store when the PUT arrives
// rather than by anything we observed. The staged route re-reads the name
// before committing, so it refuses there instead and never streams the staged
// object at all; its conditional is the backstop behind that.
func TestUploadRefusesANameRecreatedAfterItsPredecessorVanished(t *testing.T) {
	const intruder = "another writer's object, which this operator never saw"

	setup := func(t *testing.T) (*swiftcontrol.Session, *swifttest.Server, string) {
		t.Helper()
		mock := swifttest.New("test:tester", "testing")
		mock.AddContainer("media", "standard")
		seed(mock, "media", "docs/object", "the predecessor the operator acknowledged")
		server := httptest.NewServer(mock)
		t.Cleanup(server.Close)
		s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
		if err != nil {
			t.Fatal(err)
		}
		return s, mock, writeFile(t, t.TempDir(), "object", "the bytes this job planned to send")
	}

	// vanishThenRecreate is the sequence under test: the acknowledged object is
	// gone when the run looks, and a different one is at the name by the time
	// the publication is issued. It returns what the run observed.
	vanishThenRecreate := func(t *testing.T, s *swiftcontrol.Session, mock *swifttest.Server, item UploadItem) (bool, []ObservedPredecessor) {
		t.Helper()
		if err := s.DeleteObject(t.Context(), "media", "docs/object"); err != nil {
			t.Fatalf("the fixture could not remove the predecessor: %v", err)
		}
		var recorded []ObservedPredecessor
		run := &uploadRun{report: &Report{}, recorder: &UploadRecorder{
			Replacing: func(_ UploadItem, observed ObservedPredecessor) error {
				recorded = append(recorded, observed)
				return nil
			}}}
		present, err := run.observePredecessor(t.Context(), s, "media", item)
		if err != nil {
			t.Fatalf("observing the predecessor: %v", err)
		}
		if present {
			t.Fatal("the fixture did not remove the predecessor before the run looked")
		}
		if len(recorded) != 1 || recorded[0].Present {
			t.Fatalf("the run did not record an absent predecessor: %+v", recorded)
		}
		seed(mock, "media", "docs/object", intruder)
		return present, recorded
	}

	// intact asserts the recreated object still holds the intruder's bytes,
	// which is the whole point: a refusal that still overwrote would pass every
	// assertion about its own result.
	intact := func(t *testing.T, s *swiftcontrol.Session) {
		t.Helper()
		detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
		if err != nil {
			t.Fatalf("the recreated object is gone: %v", err)
		}
		sum := md5.Sum([]byte(intruder))
		if got := strings.Trim(detail.ETag, `"`); !strings.EqualFold(got, hex.EncodeToString(sum[:])) {
			t.Errorf("the recreated object was overwritten: entity tag %s, want the intruder's %x", got, sum)
		}
	}

	t.Run("native, refused by the create conditional at the store", func(t *testing.T) {
		s, mock, path := setup(t)
		item := replacingItem(t, s, path)
		present, _ := vanishThenRecreate(t, s, mock, item)

		witness := uploadWitness(t, mock)
		before := uploadSnapshot(t, witness)
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		// No comparison is needed or made: observePredecessor found the name
		// already absent, so this publication is a create and ADR 0085 keeps
		// that transition. What refuses it is the create conditional at the
		// store, which is this case's subject.
		result := nativeUpload(t.Context(), s, "media", item, file, present, nil)
		if result.Result != transfercopy.ResultConflict {
			t.Fatalf("a name recreated after its predecessor vanished was published over: %+v", result.Observation)
		}
		intact(t, s)
		if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
			t.Errorf("the refused publication changed the account: %s", diff)
		}
	})

	t.Run("staged, refused before the staged object is read", func(t *testing.T) {
		s, mock, path := setup(t)
		item := replacingItem(t, s, path)
		item.Route = "engine"
		stage := ".aeronautes/stage/vanished/batch/object"
		seed(mock, "media", stage, "the bytes this job planned to send")
		present, _ := vanishThenRecreate(t, s, mock, item)

		witness := uploadWitness(t, mock)
		before := uploadSnapshot(t, witness)
		result := commitStagedUpload(t.Context(), s, "media", stage, item, present, nil, nil)
		if result.Result != transfercopy.ResultConflict {
			t.Fatalf("a name recreated after its predecessor vanished was committed over: %+v", result.Observation)
		}
		intact(t, s)
		if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
			t.Errorf("the refused commit changed the account: %s", diff)
		}
	})

	// The control: when the predecessor is still there, the acknowledged
	// replacement proceeds. Without this the cases above would be satisfied by
	// refusing every replacement.
	t.Run("an acknowledged replacement whose predecessor is still there proceeds", func(t *testing.T) {
		s, mock, path := setup(t)
		item := replacingItem(t, s, path)
		run := &uploadRun{report: &Report{}, recorder: &UploadRecorder{
			Replacing: func(UploadItem, ObservedPredecessor) error { return nil }}}
		present, err := run.observePredecessor(t.Context(), s, "media", item)
		if err != nil || !present {
			t.Fatalf("the predecessor was not observed: present=%v err=%v", present, err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		// The predecessor IS there, so this publication really is a replacement
		// and the comparison applies. It is given one that agrees, because this
		// case is the control for the two above rather than a case about the
		// comparison.
		if result := nativeUpload(t.Context(), s, "media", item, file, present,
			comparesAgainstLive(t, s, "media", item)); result.Result != transfercopy.ResultCopied {
			t.Fatalf("an acknowledged replacement was refused: %+v", result.Observation)
		}
		_ = mock
	})
}
