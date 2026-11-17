## Approval hold matrix
| run | outcome | client ms | progress seen | proxy audit status |
|---|---|---|---|---|
| py-default-cmt300-legacy | ok | 300026 | 0 | client-gone-during-hold,ok |
| py-default-cmt300-modern | ok | 300020 | 0 | client-gone-during-hold,ok |
| py-default-cmt70-legacy | ok | 70033 | 0 | client-gone-during-hold,ok |
| py-default-cmt70-modern | ok | 70020 | 0 | client-gone-during-hold,ok |
| py-default-off10-legacy | ok | 10028 | 0 | ok |
| py-default-off10-modern | ok | 10022 | 0 | ok |
| py-default-off300-legacy | no result line | | | |
| py-default-off300-modern | no result line | | | |
| py-default-off70-legacy | ok | 70028 | 0 | client-gone-during-hold,ok |
| py-default-off70-modern | ok | 70017 | 0 | client-gone-during-hold,ok |
| py-progress-prog300-legacy | ok | 300022 | 60 | client-gone-during-hold,ok |
| py-progress-prog300-modern | ok | 300019 | 60 | client-gone-during-hold,ok |
| py-progress-prog70-legacy | ok | 70028 | 14 | client-gone-during-hold,ok |
| py-progress-prog70-modern | ok | 70020 | 14 | client-gone-during-hold,ok |
| ts-default-cmt300-legacy | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60005 | | client-gone-during-hold,ok |
| ts-default-cmt300-modern | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60006 | | client-gone-during-hold,ok |
| ts-default-cmt70-legacy | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60003 | | client-gone-during-hold,ok |
| ts-default-cmt70-modern | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60006 | | client-gone-during-hold,ok |
| ts-default-off10-legacy | ok | 10018 | 0 | ok |
| ts-default-off10-modern | ok | 10024 | 0 | ok |
| ts-default-off300-legacy | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60004 | | client-gone-during-hold |
| ts-default-off300-modern | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60006 | | client-gone-during-hold |
| ts-default-off70-legacy | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60008 | | client-gone-during-hold,ok |
| ts-default-off70-modern | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60004 | | client-gone-during-hold,ok |
| ts-progress+reset-prog300-legacy | ok | 300017 | 60 | client-gone-during-hold,ok |
| ts-progress+reset-prog300-modern | ok | 300024 | 60 | client-gone-during-hold,ok |
| ts-progress+reset-prog70-legacy | ok | 70020 | 14 | client-gone-during-hold,ok |
| ts-progress+reset-prog70-modern | ok | 70030 | 14 | client-gone-during-hold,ok |
| ts-progress-noreset-prog300-legacy | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60010 | | client-gone-during-hold,ok |
| ts-progress-noreset-prog300-modern | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60006 | | client-gone-during-hold,ok |
| ts-progress-noreset-prog70-legacy | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60004 | | client-gone-during-hold,ok |
| ts-progress-noreset-prog70-modern | FAIL SdkError REQUEST_TIMEOUT: Request timed out | 60008 | | client-gone-during-hold,ok |

