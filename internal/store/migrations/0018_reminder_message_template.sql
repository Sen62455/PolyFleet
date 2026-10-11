ALTER TABLE notification_reminder_rules
    ADD COLUMN message_template TEXT NOT NULL DEFAULT '' CHECK (length(message_template) <= 2000);
