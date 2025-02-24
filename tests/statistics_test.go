package tests

import (
	"testing"
	"gopkg.in/resty.v1"
	datastructures "github.com/bbernhard/imagemonkey-core/datastructures"
	"os/exec"
	"time"
	"os"
)

func runStatWorker(t *testing.T) {
	// Start a process:
	cmd := exec.Command("go", "run", "statworker.go", "-singleshot=true", "-redis_address="+REDIS_ADDRESS)
	cmd.Dir = "../src"
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	ok(t, err)

	// Wait for the process to finish or kill it after a timeout:
	done := make(chan error, 1)
	go func() {
	    done <- cmd.Wait()
	}()
	select {
	case <-time.After(60 * time.Second):
	    err := cmd.Process.Kill()
	    ok(t, err) //failed to kill process
	    t.Errorf("process killed as timeout reached")
	case err := <-done:
	    ok(t, err)
	}
}

type Statistics struct {
	Statistics []datastructures.DataPoint `json:"statistics"`
	Period string `json:"period"`
}

func testGetDonationsStatistics(t *testing.T) Statistics {
	var statistics Statistics
	u := BASE_URL + API_VERSION + "/statistics/donations"

	req := resty.R().
				SetResult(&statistics)

	resp, err := req.Get(u)

	ok(t, err)
    equals(t, resp.StatusCode(), 200)

    return statistics
}

func testGetStatistics(t *testing.T) datastructures.Statistics {
	var statistics datastructures.Statistics

	u := BASE_URL + API_VERSION + "/statistics"

	req := resty.R().
				SetResult(&statistics)

	resp, err := req.Get(u)

	ok(t, err)
    equals(t, resp.StatusCode(), 200)

    return statistics
}

func testGetAnnotatedStatistics(t *testing.T, token string, requiredNumOfResults int) []datastructures.AnnotatedStat {
	var annotationStatistics []datastructures.AnnotatedStat

	u := BASE_URL + API_VERSION + "/statistics/annotated"
	req := resty.R().
				SetResult(&annotationStatistics)

	if token != "" {
		req.SetAuthToken(token)
	}

	resp, err := req.Get(u)

	ok(t, err)
    equals(t, resp.StatusCode(), 200)

    equals(t, len(annotationStatistics), requiredNumOfResults)

    return annotationStatistics
}

func TestGetAnnotatedStatistics(t *testing.T) {
	teardownTestCase := setupTestCase(t)
	defer teardownTestCase(t)

	testDonate(t, "./images/apples/apple1.jpeg", "apple", true, "", "", 200)

	imageId, err := db.GetLatestDonatedImageId()
	ok(t, err)

	testAnnotate(t, imageId, "apple", "", 
					`[{"top":60,"left":145,"type":"rect","angle":0,"width":836,"height":660,"stroke":{"color":"red","width":5}}]`, "", 201)


	annotationStatistics := testGetAnnotatedStatistics(t, "", 1)

	annotationStatistics[0].Label.Name = "apple"
	annotationStatistics[0].Num.Completed = 1
	annotationStatistics[0].Num.Total = 1
}

func TestGetAnnotatedStatistics1(t *testing.T) {
	teardownTestCase := setupTestCase(t)
	defer teardownTestCase(t)

	testDonate(t, "./images/apples/apple1.jpeg", "apple", true, "", "", 200)

	imageId, err := db.GetLatestDonatedImageId()
	ok(t, err)

	testLabelImage(t, imageId, "orange", "")

	testAnnotate(t, imageId, "apple", "", 
					`[{"top":60,"left":145,"type":"rect","angle":0,"width":836,"height":660,"stroke":{"color":"red","width":5}}]`, "", 201)


	annotationStatistics := testGetAnnotatedStatistics(t, "", 2)

	annotationStatistics[0].Label.Name = "orange"
	annotationStatistics[0].Num.Completed = 0
	annotationStatistics[0].Num.Total = 1


	annotationStatistics[1].Label.Name = "apple"
	annotationStatistics[1].Num.Completed = 1
	annotationStatistics[1].Num.Total = 1
}

func TestGetPerCountryStatisticsEmpty(t *testing.T) {
	teardownTestCase := setupTestCase(t)
	defer teardownTestCase(t)
	
	statistics := testGetStatistics(t)

	equals(t, len(statistics.DonationsPerCountry), 0)
	equals(t, len(statistics.ValidationsPerCountry), 0)
	equals(t, len(statistics.AnnotationsPerCountry), 0)
	equals(t, len(statistics.AnnotationRefinementsPerCountry), 0)
	equals(t, len(statistics.ImageDescriptionsPerCountry), 0)
}

func TestGetPerCountryStatistics(t *testing.T) {
	runStatWorker(t) //clear pending statistics

	teardownTestCase := setupTestCase(t)
	defer teardownTestCase(t)

	testDonate(t, "./images/apples/apple1.jpeg", "apple", true, "", "", 200)

	statistics := testGetStatistics(t)

	equals(t, len(statistics.DonationsPerCountry), 0)
	equals(t, len(statistics.ValidationsPerCountry), 0)
	equals(t, len(statistics.AnnotationsPerCountry), 0)
	equals(t, len(statistics.AnnotationRefinementsPerCountry), 0)
	equals(t, len(statistics.ImageDescriptionsPerCountry), 0)

	imageId, err := db.GetLatestDonatedImageId()
	ok(t, err)

	var imageDescriptions []datastructures.ImageDescription
	imageDescription := datastructures.ImageDescription{Description: "apple on the desk", Language: "en"}
	imageDescription2 := datastructures.ImageDescription{Description: "Apfel am Boden", Language: "ger"}
	imageDescriptions = append(imageDescriptions, imageDescription)
	imageDescriptions = append(imageDescriptions, imageDescription2)

	testAddImageDescriptions(t, imageId, imageDescriptions)

	runStatWorker(t)

	statistics = testGetStatistics(t)


	equals(t, statistics.DonationsPerCountry[0].Count, int64(1))
	equals(t, len(statistics.ValidationsPerCountry), 0)
	equals(t, len(statistics.AnnotationsPerCountry), 0)
	equals(t, len(statistics.AnnotationRefinementsPerCountry), 0)
	equals(t, statistics.ImageDescriptionsPerCountry[0].Count, int64(2))
}

func TestGetDonationsStatistics(t *testing.T) {
	teardownTestCase := setupTestCase(t)
	defer teardownTestCase(t)

	statisticsBefore := testGetDonationsStatistics(t)

	numDays, err := db.GetNumOfDatesFromNowTilOneMonthAgo()
	ok(t, err)

	equals(t, len(statisticsBefore.Statistics), numDays)

	testDonate(t, "./images/apples/apple1.jpeg", "apple", true, "", "", 200)

	statisticsAfter := testGetDonationsStatistics(t)
	equals(t, len(statisticsAfter.Statistics), numDays)

	currentTime := time.Now()
	for _, val := range statisticsAfter.Statistics {
		if currentTime.Format("2006-01-02") == val.Date {
			equals(t, int(val.Value), int(1))
		} else {
			equals(t, int(val.Value), int(0))
		}
	}
}
