package main

import (
	log "github.com/sirupsen/logrus"
	"flag"
	"github.com/gofrs/uuid"
	"database/sql"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
	"context"
	datastructures "github.com/bbernhard/imagemonkey-core/datastructures"
	commons "github.com/bbernhard/imagemonkey-core/commons"
	imagemonkeydb "github.com/bbernhard/imagemonkey-core/database"

)

var db *sql.DB

func trendingLabelExists(label string, tx *sql.Tx) (bool, error) {
	var numOfRows int32
	err := tx.QueryRow(`SELECT COUNT(*) FROM trending_label_suggestion t 
			  			RIGHT JOIN label_suggestion l ON t.label_suggestion_id = l.id
			  			WHERE l.name = $1`, label).Scan(&numOfRows)
	if err != nil {
		return false, err
	}

	if numOfRows > 0 {
		return true, nil
	}

	return false, nil
}

func getLabelId(labelIdentifier string, tx *sql.Tx) (int64, error) {
	var labelId int64 = -1
	var err error
	var rows *sql.Rows

	_, err = uuid.FromString(labelIdentifier)
	if err == nil { //is UUID
		rows, err = tx.Query(`SELECT l.id 
						   FROM label l 
			  			   WHERE l.uuid::text = $1`, labelIdentifier)
	} else {
		rows, err = tx.Query(`SELECT l.id 
						   FROM label l 
			  			   WHERE l.name = $1 AND l.parent_id is null`, labelIdentifier)
	}

	if err != nil {
		return -1, err
	}
	defer rows.Close()

	if rows.Next() {
		err = rows.Scan(&labelId)
		if err != nil {
			return -1, err
		}
	}
	return labelId, nil
}

func getLabelMeEntryFromUuid(tx *sql.Tx, uuid string) (datastructures.LabelMeEntry, error) {
	var labelMeEntry datastructures.LabelMeEntry

	rows, err := tx.Query(`SELECT l.name, COALESCE(pl.name, '')
			   				FROM label l
			   				LEFT JOIN label pl ON pl.id = l.parent_id
			   				WHERE l.uuid::text = $1`, uuid)
	if err != nil {
		return labelMeEntry, err
	}
	defer rows.Close()

	var label1 string
	var label2 string
	if rows.Next() {
		err = rows.Scan(&label1, &label2)
		if err != nil {
			return labelMeEntry, err
		}

		if label2 == "" {
            labelMeEntry.Label = label1
        } else {
            labelMeEntry.Label = label2
            labelMeEntry.Sublabels = append(labelMeEntry.Sublabels,
								datastructures.Sublabel{Name: label1})
        }
	}

	return labelMeEntry, nil
}

func removeTrendingLabelEntries(trendingLabel string, tx *sql.Tx) (error) {
	_, err := tx.Exec(`DELETE FROM image_label_suggestion s
					   WHERE s.label_suggestion_id IN (
					   	SELECT l.id FROM label_suggestion l WHERE l.name = $1
					   )`, trendingLabel)

	if err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE trending_label_suggestion t 
				 	  SET closed = true
				 	  FROM label_suggestion AS l 
				 	  WHERE label_suggestion_id = l.id AND l.name = $1`, trendingLabel)

	return err
}

func closeGithubIssue(trendingLabel string, repository string, githubProjectOwner string, githubApiToken string, tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT t.github_issue_id, t.closed
							FROM trending_label_suggestion t
							JOIN label_suggestion l ON t.label_suggestion_id = l.id
							WHERE l.name = $1`, trendingLabel)
	if err != nil {
		return err
	}

	defer rows.Close()

	if rows.Next() {
		var githubIssueId int
		var issueClosed bool

		err = rows.Scan(&githubIssueId, &issueClosed)
		if err != nil {
			return err
		}

		//if !issueClosed {
			ctx := context.Background()
			ts := oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: githubApiToken},
			)
			tc := oauth2.NewClient(ctx, ts)

			client := github.NewClient(tc)

			body := "label is now productive."

			//create a new comment
			commentRequest := &github.IssueComment{
				Body:    github.String(body),
			}

			//we do not care whether we can successfully close the github issue..if it doesn't work, one can always close it
			//manually.
			_, _, err = client.Issues.CreateComment(ctx, githubProjectOwner, repository, githubIssueId, commentRequest)
			if err == nil { //if comment was successfully created, close issue
				issueRequest := &github.IssueRequest{
					State: github.String("closed"),
				}

				_, _, err = client.Issues.Edit(ctx, githubProjectOwner, repository, githubIssueId, issueRequest)
				if err != nil {
					log.Info("[Main] Couldn't close github issue, please close manually!")
				}
			} else {
				log.Info("[Main] Couldn't close github issue, please close manually!")
			}
		//}
	}

	return nil
}


func makeTrendingLabelProductive(trendingLabel string, label datastructures.LabelMeEntry,
									labelId int64, tx *sql.Tx) error {
	type Result struct {
		ImageId string
		Annotatable bool
	}

    rows, err := tx.Query(`SELECT i.key, annotatable 
			  			   FROM label_suggestion l
			  			   JOIN image_label_suggestion isg on isg.label_suggestion_id =l.id
			  			   JOIN image i on i.id = isg.image_id
			  			   WHERE l.name = $1`, trendingLabel)
    if err != nil {
		return err
    }

    defer rows.Close()

    //due to a bug in the pq driver (see https://github.com/lib/pq/issues/81)
    //we need to first close the rows before we can execute another transaction.
    //that means we need to store the result set temporarily in a list
	var results []Result
    for rows.Next() {
		var result Result

		err = rows.Scan(&result.ImageId, &result.Annotatable)
		if err != nil {
			return err
		}

		results = append(results, result)
    }

    rows.Close()

    var labels []datastructures.LabelMeEntry
    labels = append(labels, label)

    for _, elem := range results {
		numNonAnnotatable := 0
		if !elem.Annotatable {
			numNonAnnotatable = 10
		}

		_, err = imagemonkeydb.AddLabelsToImageInTransaction("", elem.ImageId, labels, 0, numNonAnnotatable, tx)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(`UPDATE trending_label_suggestion t
						SET productive_label_id = $2
						FROM label_suggestion l
						WHERE t.label_suggestion_id = l.id AND l.name = $1`, trendingLabel, labelId)
	if err != nil {
		return err
	}

    return nil
}

func makeLabelMeEntry(tx *sql.Tx, name string) (datastructures.LabelMeEntry, error) {
    _, err := uuid.FromString(name)
    if err == nil { //is UUID
		entry, err := getLabelMeEntryFromUuid(tx, name)
		if err != nil {
			return datastructures.LabelMeEntry{}, err //UUID is not in database
		}
		return entry, nil
    }

	//not a UUID	
	var label datastructures.LabelMeEntry
	label.Label = name
	label.Sublabels = []datastructures.Sublabel{}

    return label, nil
}

func isLabelInLabelsMap(labelMap map[string]datastructures.LabelMapEntry, metalabels *commons.MetaLabels, label datastructures.LabelMeEntry) bool {
	return commons.IsLabelValid(labelMap, metalabels, label.Label, label.Sublabels)
}


func getNonStrictLabels(tx *sql.Tx, name string) ([]string, error) {
	names := []string{}

	rows, err := tx.Query("SELECT l.name FROM label_suggestion l WHERE l.name ~  ('^[ ]*'||$1||'[ ]*$')", name)
	if err != nil {
		return names, err
	}

	defer rows.Close()

	for rows.Next() {
		var n string
		err = rows.Scan(&n)
		if err != nil {
			return names, err
		}

		names = append(names, n)
	}

	return names, nil
}

func main() {
	log.SetLevel(log.DebugLevel)

	trendingLabel := flag.String("trendinglabel", "", "The name of the trending label that should be made productive")
	renameTo := flag.String("renameto", "", "Rename the label")
	wordlistPath := flag.String("wordlist", "../wordlists/en/labels.jsonnet", "Path to label map")
	metalabelsPath := flag.String("metalabels", "../wordlists/en/metalabels.jsonnet", "Path to metalabels map")
	dryRun := flag.Bool("dryrun", true, "Specifies whether this is a dryrun or not")
	autoCloseIssue := flag.Bool("autoclose", true, "Automatically close issue")
	githubRepository := flag.String("repository", "", "Github repository")
	strict := flag.Bool("strict", false, "strict label matching")

	flag.Parse()

	githubProjectOwner := ""
	githubApiToken := ""
	if *autoCloseIssue {
		githubProjectOwner = commons.MustGetEnv("GITHUB_PROJECT_OWNER")
		githubApiToken = commons.MustGetEnv("GITHUB_API_TOKEN")
	}

	if *autoCloseIssue && *githubRepository == "" {
		log.Fatal("Please set a valid repository!")
	}

	labelRepository := commons.NewLabelRepository()
	err := labelRepository.Load(*wordlistPath)
	if err != nil {
		log.Fatal("[Main] Couldn't read label map...terminating!")
	}
	labelMap := labelRepository.GetMapping()

	metaLabels := commons.NewMetaLabels(*metalabelsPath)
	err = metaLabels.Load()
	if err != nil {
		log.Fatal("[Main] Couldn't read metalabel map...terminating!")
	}

	if *trendingLabel == "" {
		log.Fatal("[Main] Please provide a trending label!")
	}

	imageMonkeyDbConnectionString := commons.MustGetEnv("IMAGEMONKEY_DB_CONNECTION_STRING")
	db, err = sql.Open("postgres", imageMonkeyDbConnectionString)
	if err != nil {
		log.Fatal(err)
	}

	err = db.Ping()
	if err != nil {
		log.Fatal("[Main] Couldn't ping database: ", err.Error())
	}
	defer db.Close()


	tx, err := db.Begin()
    if err != nil {
		log.Fatal("[Main] Couldn't begin trensaction: ", err.Error())
    }


	trendingLabels := []string{}
	if *strict {
		exists, err := trendingLabelExists(*trendingLabel, tx)
		if err != nil {
			tx.Rollback()
			log.Fatal("[Main] Couldn't determine whether trending label exists: ", err.Error())
		}

		if exists {
			trendingLabels = append(trendingLabels, *trendingLabel)
		}
	} else {
		nonStrictLabels, err := getNonStrictLabels(tx, *trendingLabel)
		if err != nil {
			tx.Rollback()
			log.Fatal("Couldn't get non strict labels: ", err.Error())
		}
		trendingLabels = nonStrictLabels
	}

	if len(trendingLabels) == 0 {
		log.Fatal("[Main] Trending label doesn't exist. Maybe a typo?")
	}

	labelToCheck := *trendingLabel
	if *renameTo != "" {
		labelToCheck = *renameTo
	}


	labelId, err := getLabelId(labelToCheck, tx)
	if err != nil {
		tx.Rollback()
		log.Fatal("[Main] Couldn't determine whether label exists: ", err.Error())
	}
	if labelId == -1 {
		tx.Rollback()
		log.Fatal("[Main] label ", labelToCheck, " doesn't exist in database - please add it via the populate_labels script.")
	}


	labelMeEntry, err := makeLabelMeEntry(tx, labelToCheck)
	if err != nil {
		tx.Rollback()
		log.Fatal("[Main] Couldn't create label entry - is UUID valid?")
	}

	if !isLabelInLabelsMap(labelMap, metaLabels, labelMeEntry) && *renameTo == "" {
		tx.Rollback()
		log.Fatal("[Main] Label doesn't exist in labels map - please add it first!")
	}

	for _, trendingLabel := range trendingLabels {
		err = makeTrendingLabelProductive(trendingLabel, labelMeEntry, labelId, tx)
		if err != nil {
			tx.Rollback()
			log.Fatal("[Main] Couldn't make trending label ", trendingLabel, " productive: ", err.Error())
		}

		err = removeTrendingLabelEntries(trendingLabel, tx)
		if err != nil {
			tx.Rollback()
			log.Fatal("[Main] Couldn't remove trending label entries: ", err.Error())
		}
	}

    if *dryRun {
		err = tx.Rollback()
		if err != nil {
			log.Fatal("[Main] Couldn't rollback transaction: ", err.Error())
		}

		log.Info("[Main] This was just a dry run - rolling back everything")
    } else {
		//only handle autoclose, in case it's not a dry run
		if *autoCloseIssue {
			for _, trendingLabel := range trendingLabels {
				err := closeGithubIssue(trendingLabel, *githubRepository, githubProjectOwner, githubApiToken, tx)
				if err != nil {
					tx.Rollback()
					log.Fatal("[Main] Couldn't get github issue id to close issue!")
				}
			}
		}

		err = tx.Commit()
		if err != nil {
			log.Fatal("[Main] Couldn't commit transaction: ", err.Error())
		}

		autoCloseIssueStr := ""
		if !*autoCloseIssue {
			autoCloseIssueStr = "You can now close the corresponding github issue!"
		}

		for _, trendingLabel := range trendingLabels {
			if trendingLabel == labelMeEntry.Label {
				log.Info("[Main] Label ", trendingLabel, " was successfully made productive. ", autoCloseIssueStr)
			} else {
				log.Info("[Main] Label ", trendingLabel, " was renamed to ", labelMeEntry.Label, " successfully made productive. ", autoCloseIssueStr)
			}
		}
	}
}
