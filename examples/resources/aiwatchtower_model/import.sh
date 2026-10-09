# Models are imported as user_name@model_name@model_version. The identifier is split from the
# right, so a user name that is an email address imports correctly.
terraform import aiwatchtower_model.example 'user@company.com@Fraud-Guard@V1.0.0'
