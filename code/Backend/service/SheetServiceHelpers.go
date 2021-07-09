package service

import (
	"backend/dao"
	"backend/lib/cache"
	"backend/lib/gdocFS"
	"backend/utils"
	"backend/utils/logger"
	"encoding/json"
	"github.com/pkg/errors"
	"strconv"
	"strings"
	"time"
)

const (
	minRows = 10
	minCols = 10
)

var (
	logCommitEntry = gdocFS.SheetLogPickle{
		Row: -1,
		Col: -1,
	}
)

var (
	SheetFSUnrecoverableErr = errors.New("sheet filesystem is not consistent and unrecoverable")
)

// SheetFSCheck checks the consistency of sheet filesystem (fullChk: THOROUGHLY, !fullChk: BRIEFLY)
//   and make best efforts to recover it.
// A file used to be handled by a crashed server should be checked THOROUGHLY, otherwise use SheetBriefFSCheck instead.
// If the sheet filesystem is consistent, which means -- (lid == cid + 1) && (log end with commit entry),
//   current maximum cid and lid are returned.
// Or if the sheet filesystem is inconsistent and cannot not be recovered, error SheetFSUnrecoverableErr is returned.
func SheetFSCheck(fid uint, fullChk bool) (cid uint, lid uint, err error) {
	logRoot := gdocFS.GetLogRootPath("sheet", fid)
	chkpRoot := gdocFS.GetCheckPointRootPath("sheet", fid)

	// check log-only consistency
	logFileNames, err := dao.DirFilenamesAllSorted(logRoot)
	if err != nil {
		return 0, 0, err
	}

	expectLid := uint(len(logFileNames))
	for expect, actual := range logFileNames {
		curLid := uint(expect + 1)
		// check name == curLid without holes
		if strconv.Itoa(int(curLid)) != actual {
			// TODO: recover - hole in log files
			return 0, 0, SheetFSUnrecoverableErr
		}

		if fullChk {	// fullChk: check log is valid and committed
			if logs, err := sheetGetPickledLogFromDfs(fid, curLid); err != nil {
				// TODO: recover - log is invalid
				return 0, 0, SheetFSUnrecoverableErr
			} else if lastLog := logs[len(logs)-1]; lastLog != logCommitEntry {
				if curLid == expectLid {	// last log uncommitted can be recovered by simply committing it
					// TODO: !!! recover last uncommitted log !!!
				} else {					// middle log uncommitted can be recovered?
					// TODO: recover - log is uncommitted
					return 0, 0, SheetFSUnrecoverableErr
				}

				for _, log := range logs {
					if log.Lid != curLid || log.Row <= 0 || log.Col <= 0 {
						// TODO: recover - log is invalid
						return 0, 0, SheetFSUnrecoverableErr
					}
				}
			}
		}
	}
	if !fullChk {	// !fullChk: check last log is committed
		if logs, err := sheetGetPickledLogFromDfs(fid, expectLid); err != nil {
			// TODO: recover - log is invalid
			return 0, 0, SheetFSUnrecoverableErr
		} else if lastLog := logs[len(logs)-1]; lastLog != logCommitEntry {
			// TODO: !!! recover last uncommitted log !!!
		}
	}

	// check checkpoint-only consistency
	chkpFileNames, err := dao.DirFilenamesAllSorted(chkpRoot)
	if err != nil {
		return 0, 0, err
	}

	expectCid := uint(len(chkpFileNames))
	for expect, actual := range chkpFileNames {
		curCid := uint(expect) + 1
		// check name == curCid without holes
		if strconv.Itoa(int(curCid)) != actual {
			// TODO: recover - hole in checkpoint files
			return 0, 0, SheetFSUnrecoverableErr
		}

		if fullChk {	// fullChk: check checkpoint is valid
			if chkp, err := sheetGetPickledCheckPointFromDfs(fid, curCid); err != nil ||
				chkp.Cid != curCid || chkp.Rows <= 0 || chkp.Columns <= 0 {
				// TODO: recover - checkpoint is invalid
				return 0, 0, SheetFSUnrecoverableErr
			}
		}
	}

	// check consistency between log and checkpoint
	if expectCid + 1 != expectLid {
		// TODO: recover - cid + 1 != lid
		return 0, 0, SheetFSUnrecoverableErr
	}

	return expectCid, expectLid, nil
}

func appendOneSheetLog(fid uint, lid uint, log *gdocFS.SheetLogPickle) {
	path := gdocFS.GetLogPath("sheet", fid, lid)
	fileRawByte, _ := json.Marshal(*log)
	fileRaw := string(fileRawByte)
	if err := dao.FileAppend(path, fileRaw); err != nil {
		logger.Errorf("[%s] Log file append fails!\n%+v", path, err)
		return
	}
}

func commitOneSheetWithCache(fid uint, memSheet *cache.MemSheet) (cid uint) {
	memSheet.Lock()

	// update model Sheet
	curCid := uint(sheetGetCheckPointNum(fid))

	// empty log
	lid := curCid + 1
	if logs, err := sheetGetPickledLogFromDfs(fid, lid); err != nil {
		if len(logs) == 0 {
			return curCid
		}
	}

	// write checkpoint to curCid+1
	cid = curCid + 1
	rows, cols := memSheet.Shape()
	if err := sheetCreatePickledCheckPointInDfs(fid, cid, &gdocFS.SheetCheckPointPickle{
		Cid: cid,
		Timestamp: time.Now(),
		Rows: rows,
		Columns: cols,
		Content: memSheet.ToStringSlice(),
	}); err != nil {
		logger.Errorf("%+v", err)
	}

	// write commit entry to log with lid=curCid+1
	appendOneSheetLog(fid, lid, &logCommitEntry)

	// create log with lid=curCid+2
	if err := sheetCreateLogFile(fid, lid + 1); err != nil {
		logger.Errorf("%+v", err)
	}

	memSheet.Unlock()

	return cid
}

func commitSheetsWithCache(fids []uint, memSheets []*cache.MemSheet) {
	for ei := 0; ei < len(fids); ei += 1 {
		fid, memSheet := fids[ei], memSheets[ei]
		commitOneSheetWithCache(fid, memSheet)
	}
}

// When calling recoverSheetFromLog, log file must end with commit entry because log would be committed automatically
//   when all users quit editing or sheet is evicted from memCache.
// BUT log can be *UNCOMMITTED* if the server it belonged to crashed, for which we need to thoroughly handle
//   all possible circumstances here in order to achieve crash consistency.
func recoverSheetFromLog(fid uint) (memSheet *cache.MemSheet, inCache bool) {
	curCid := uint(sheetGetCheckPointNum(fid))

	// TODO: determine whether sheet is from crashed server and call SheetFSCheck
	// SheetFSCheck(fid, isFromCrashServer)

	// get memSheet from scratch or latest checkpoint
	if curCid == 0 {
		memSheet = cache.NewMemSheet(minRows, minCols)
	} else {
		if chkp, err := sheetGetPickledCheckPointFromDfs(fid, curCid); err != nil {
			logger.Errorf("[%d] %+v", fid, err)
			return nil, false
		} else {
			memSheet = cache.NewMemSheetFromStringSlice(chkp.Content, chkp.Columns)
		}
	}

	// redo with latest log
	if logs, err := sheetGetPickledLogFromDfs(fid, curCid + 1); err != nil {
		logger.Errorf("%+v", err)
		return nil, false
	} else {
		for li := 0; li < len(logs) - 1; li += 1 {	// without logCommitEntry, which is in the end
			log := &logs[li]
			memSheet.Set(log.Row, log.Col, log.New)
		}

		// do eviction
		if ms, keys, evicted := getSheetCache().Add(fid, memSheet); ms != nil {
			commitSheetsWithCache(utils.InterfaceSliceToUintSlice(keys), evicted)
			return ms, true
		} else {
			return memSheet, false
		}
	}
}

// sheetGetPickledCheckPointFromDfs pickles a CheckPoint from dfs with fid and cid
func sheetGetPickledCheckPointFromDfs(fid uint, cid uint) (chkp *gdocFS.SheetCheckPointPickle, err error) {
	path := gdocFS.GetCheckPointPath("sheet", fid, cid)
	if fileRaw, err := dao.FileGetAll(path); err != nil {
		return nil, errors.WithStack(err)
	} else {
		chkp, err = gdocFS.PickleSheetCheckPointFromContent(fileRaw)
		return chkp, errors.WithStack(err)
	}
}

// sheetWritePickledCheckPointToDfs writes a CheckPoint to a EXISTENT file in dfs with fid and cid
func sheetWritePickledCheckPointToDfs(fid uint, cid uint, chkp *gdocFS.SheetCheckPointPickle) (err error) {
	path := gdocFS.GetCheckPointPath("sheet", fid, cid)
	fileRaw, _ := json.Marshal(*chkp)
	if err = dao.FileOverwriteAll(path, string(fileRaw)); err != nil {
		return errors.WithStack(err)
	} else {
		return nil
	}
}

// sheetCreatePickledCheckPointInDfs create a CheckPoint in a NONEXISTENT file in dfs with fid and cid
func sheetCreatePickledCheckPointInDfs(fid uint, cid uint, chkp *gdocFS.SheetCheckPointPickle) (err error) {
	path := gdocFS.GetCheckPointPath("sheet", fid, cid)
	if err := dao.FileCreate(path, 0); err != nil {
		return errors.WithStack(err)
	} else {
		return sheetWritePickledCheckPointToDfs(fid, cid, chkp)
	}
}

// sheetCreateCheckPointDir create a empty checkpoint directory in dfs with fid
func sheetCreateCheckPointDir(fid uint) (err error) {
	chkpRoot := gdocFS.GetCheckPointRootPath("sheet", fid)
	if err := dao.DirCreate(chkpRoot); err != nil {
		return err
	} else {
		return nil
	}
}

// sheetDeleteCheckPointFile delete a checkpoint file in dfs with fid and cid
func sheetDeleteCheckPointFile(fid uint, cid uint) (err error)  {
	chkpPath := gdocFS.GetCheckPointPath("sheet", fid, cid)
	if err := dao.Remove(chkpPath); err != nil {
		return err
	} else {
		return nil
	}
}

func sheetGetCheckPointNum(fid uint) (chkpNum int) {
	path := gdocFS.GetCheckPointRootPath("sheet", fid)
	fileNames, err := dao.DirFilenamesAllSorted(path)
	if err != nil {
		panic(err)
	}

	if len(fileNames) != 0 {
		latestChkpName := fileNames[len(fileNames)-1]
		chkpNum, err = strconv.Atoi(strings.Split(latestChkpName, ".")[0])
		if err != nil {
			logger.Errorf("[%s] bad checkpoint name, use length of children instead", latestChkpName)
			return len(fileNames)
		}

		if len(fileNames) != chkpNum {
			logger.Errorf("[name(%s)\tchkpNum(%d)] len(children) != latest checkpoint's name, use the latter",
				latestChkpName, chkpNum)
		}
		return chkpNum
	} else {
		return 0
	}
}

// sheetGetPickledLogFromDfs pickles a Log from dfs with fid and lid
func sheetGetPickledLogFromDfs(fid uint, lid uint) (logs []gdocFS.SheetLogPickle, err error) {
	path := gdocFS.GetLogPath("sheet", fid, lid)
	if fileRaw, err := dao.FileGetAll(path); err != nil {
		return nil, errors.WithStack(err)
	} else {
		logs, err = gdocFS.PickleSheetLogsFromContent(fileRaw)
		return logs, errors.WithStack(err)
	}
}

// sheetCreateLogFile create a empty Log in dfs with fid and lid
func sheetCreateLogFile(fid uint, lid uint) (err error) {
	logPath := gdocFS.GetLogPath("sheet", fid, lid)
	if err := dao.FileCreate(logPath, 0); err != nil {
		return err
	} else {
		return nil
	}
}

// sheetDeleteLogFile delete a log file in dfs with fid and lid
func sheetDeleteLogFile(fid uint, lid uint) (err error) {
	logPath := gdocFS.GetLogPath("sheet", fid, lid)
	if err := dao.Remove(logPath); err != nil {
		return err
	} else {
		return nil
	}
}
