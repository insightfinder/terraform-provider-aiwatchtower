# Monitors are imported as user_name@monitor_name. The identifier is split from the right, so a
# user name that is an email address imports correctly.
terraform import aiwatchtower_monitor.example 'user@company.com@Fraud-Guard-DQ'
