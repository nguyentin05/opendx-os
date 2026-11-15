# Approval policy

Amounts are expressed in VND.

| Amount (VND) | Approver |
| --- | --- |
| >= 10,000,000 | Director |
| < 10,000,000 | Manager |

## Vietnamese

Nghỉ phép: Nhân viên gửi yêu cầu nghỉ phép và người quản lý phê duyệt.

```go
if amount >= 10000000 {
    approver = "Director"
}
```
